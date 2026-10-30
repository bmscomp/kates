package cmd

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/bmscomp/kates/cli/client"
	"github.com/bmscomp/kates/cli/output"
	"github.com/spf13/cobra"
)

const evidencePageSize = 200

var (
	evidenceFrom string
	evidenceTo   string
	evidenceOut  string
	evidenceSign bool
)

// evidenceFiles is the order the pack's files are written and listed in SHA256SUMS.
var evidenceFiles = []string{
	"export.json", "runs.json", "disruptions.json", "compliance-checks.json", "audit.json", "audit-verify.json",
}

var evidenceCmd = &cobra.Command{
	Use:   "evidence",
	Short: "Export what Kates recorded in a period, for an auditor",
}

var evidenceExportCmd = &cobra.Command{
	Use:   "export --from <time> --out <dir>",
	Short: "Write the test runs, disruptions, compliance checks and audit trail of a period to a directory",
	Long: `Writes everything the Kates API recorded in a period to a new directory, as
the API answered it, for an auditor: the test runs created in it, the full
report of each disruption, every stored compliance check with its report,
the audit rows, and a check of the audit chain. SHA256SUMS lists the hash of
each file, so "shasum -a 256 -c SHA256SUMS" shows that none changed.

--sign signs SHA256SUMS with cosign, keyless: cosign opens a browser to sign
in, and writes SHA256SUMS.sigstore.json, which "cosign verify-blob" checks.

The period is [--from, --to). It exits 1 when the audit chain is broken; the
directory is written all the same, and audit-verify.json says where.`,
	Example: `  kates evidence export --from 2026-07-01T00:00:00Z --to 2026-10-01T00:00:00Z --out q3-evidence
  kates evidence export --from 2026-10-01T00:00:00Z --out october --sign`,
	RunE: func(cmd *cobra.Command, args []string) error {
		from, to, err := evidencePeriod(evidenceFrom, evidenceTo, time.Now())
		if err != nil {
			return cmdErr(err.Error())
		}
		// Checked first, created only once everything is read: a failed export leaves no directory to refuse a retry.
		if _, err := os.Stat(evidenceOut); !errors.Is(err, os.ErrNotExist) {
			return cmdErr("The evidence directory " + evidenceOut + " exists already, or can't be checked: it must not exist yet")
		}
		pack, err := collectEvidence(context.Background(), apiClient, from, to)
		if err != nil {
			return cmdErr("Evidence export stopped: " + evidenceHint(err))
		}
		if err := os.Mkdir(evidenceOut, 0o750); err != nil {
			return cmdErr("Cannot create the evidence directory: " + err.Error())
		}
		if err := writeEvidence(evidenceOut, pack); err != nil {
			_ = os.RemoveAll(evidenceOut)
			return cmdErr("Cannot write the evidence, so nothing was kept: " + err.Error())
		}
		if evidenceSign {
			if err := signEvidence(context.Background(), evidenceOut); err != nil {
				return cmdErr("The evidence is written, but signing failed: " + err.Error())
			}
		}

		output.Banner("Evidence", evidenceOut)
		output.KeyValue("Period", from.Format(time.RFC3339)+" to "+to.Format(time.RFC3339))
		for _, f := range evidenceFiles[1:5] {
			output.KeyValue(f, strconv.Itoa(len(pack.lists[f]))+" items")
		}
		if !pack.chain.Intact {
			output.Warn("The audit chain is broken: " + pack.chain.Problem)
			return &exitError{code: failedExitCode, msg: "audit chain broken"}
		}
		output.Success("Audit chain intact, head " + strconv.FormatInt(pack.chain.HeadID, 10) + " " + pack.chain.HeadHash)
		return nil
	},
}

func evidencePeriod(fromFlag, toFlag string, now time.Time) (time.Time, time.Time, error) {
	from, err := time.Parse(time.RFC3339, fromFlag)
	if err != nil {
		return time.Time{}, time.Time{}, fmt.Errorf("--from must be an RFC 3339 time, as 2026-07-01T00:00:00Z")
	}
	to := now.UTC()
	if toFlag != "" {
		if to, err = time.Parse(time.RFC3339, toFlag); err != nil {
			return time.Time{}, time.Time{}, fmt.Errorf("--to must be an RFC 3339 time, as 2026-10-01T00:00:00Z")
		}
	}
	if !from.Before(to) {
		return time.Time{}, time.Time{}, fmt.Errorf("--from must be before --to")
	}
	return from.UTC(), to.UTC(), nil
}

// evidenceChain is the part of GET /api/audit/verify the command reports.
type evidenceChain struct {
	Intact   bool   `json:"intact"`
	HeadID   int64  `json:"headId"`
	HeadHash string `json:"headHash"`
	Problem  string `json:"problem"`
}

type evidencePack struct {
	export json.RawMessage
	lists  map[string][]json.RawMessage
	verify json.RawMessage
	chain  evidenceChain
}

func collectEvidence(ctx context.Context, c *client.Client, from, to time.Time) (*evidencePack, error) {
	pack := &evidencePack{lists: map[string][]json.RawMessage{}}
	var err error
	if pack.lists["runs.json"], err = newestFirstIn(ctx, c, "/api/tests", nil, from, to); err != nil {
		return nil, err
	}
	summaries, err := newestFirstIn(ctx, c, "/api/disruptions", nil, from, to)
	if err != nil {
		return nil, err
	}
	if pack.lists["disruptions.json"], err = eachByID(ctx, c, "/api/disruptions/", summaries); err != nil {
		return nil, err
	}
	period := url.Values{"since": {from.Format(time.RFC3339Nano)}, "until": {to.Format(time.RFC3339Nano)}}
	checks, err := allPages(ctx, c, "/api/conformity/checks", period)
	if err != nil {
		return nil, err
	}
	if pack.lists["compliance-checks.json"], err = eachByID(ctx, c, "/api/conformity/checks/", checks); err != nil {
		return nil, err
	}
	if pack.lists["audit.json"], err = allPages(ctx, c, "/api/audit", period); err != nil {
		return nil, err
	}
	if err := auditInPeriod(pack.lists["audit.json"], from, to); err != nil {
		return nil, err
	}
	if pack.verify, err = c.GetRaw(ctx, "/api/audit/verify", nil); err != nil {
		return nil, err
	}
	if err := json.Unmarshal(pack.verify, &pack.chain); err != nil {
		return nil, fmt.Errorf("GET /api/audit/verify: %w", err)
	}
	pack.export, err = json.MarshalIndent(map[string]any{
		"from":       from.Format(time.RFC3339Nano),
		"to":         to.Format(time.RFC3339Nano),
		"exportedAt": time.Now().UTC().Format(time.RFC3339Nano),
		"katesUrl":   c.BaseURL,
	}, "", "  ")
	return pack, err
}

// evidenceRow is what the command reads of each listed item; the item itself is kept as written.
type evidenceRow struct {
	ID        json.RawMessage `json:"id"`
	CreatedAt string          `json:"createdAt"`
}

func (r evidenceRow) key() string { return strings.Trim(string(r.ID), `"`) }

// newestFirstIn reads a list the Kates API orders newest first and has no
// period filter, keeping the items created in [from, to), until one is older.
// shortcut: an item deleted mid-read shifts the pages and can hide one; fine
// for a periodic export, worth a period filter on the endpoint if it bites.
func newestFirstIn(ctx context.Context, c *client.Client, path string, query url.Values, from, to time.Time) ([]json.RawMessage, error) {
	kept := []json.RawMessage{}
	seen := map[string]bool{}
	for page := 0; ; page++ {
		items, err := listPage(ctx, c, path, query, page)
		if err != nil {
			return nil, err
		}
		for _, item := range items {
			var row evidenceRow
			if err := json.Unmarshal(item, &row); err != nil {
				return nil, fmt.Errorf("GET %s: %w", path, err)
			}
			created, err := time.Parse(time.RFC3339Nano, row.CreatedAt)
			if err != nil {
				return nil, fmt.Errorf("GET %s: item %s has createdAt %q, not a time", path, row.key(), row.CreatedAt)
			}
			if created.Before(from) {
				return kept, nil
			}
			// A row created meanwhile pushes the pages down by one, so one may come twice.
			if created.Before(to) && !seen[row.key()] {
				seen[row.key()] = true
				kept = append(kept, item)
			}
		}
		if len(items) < evidencePageSize {
			return kept, nil
		}
	}
}

// allPages reads every page of a list the Kates API filters itself, up to
// the first short page: a total the answer lacks or gets wrong can't cut it short.
func allPages(ctx context.Context, c *client.Client, path string, query url.Values) ([]json.RawMessage, error) {
	all := []json.RawMessage{}
	for page := 0; ; page++ {
		items, err := listPage(ctx, c, path, query, page)
		if err != nil {
			return nil, err
		}
		all = append(all, items...)
		if len(items) < evidencePageSize {
			return all, nil
		}
	}
}

func listPage(ctx context.Context, c *client.Client, path string, query url.Values, page int) ([]json.RawMessage, error) {
	q := url.Values{}
	for k, v := range query {
		q[k] = v
	}
	q.Set("page", strconv.Itoa(page))
	q.Set("size", strconv.Itoa(evidencePageSize))
	data, err := c.GetRaw(ctx, path, q)
	if err != nil {
		return nil, fmt.Errorf("GET %s: %w", path, err)
	}
	var paged struct {
		Items []json.RawMessage `json:"items"`
	}
	if err := json.Unmarshal(data, &paged); err != nil {
		return nil, fmt.Errorf("GET %s: %w", path, err)
	}
	return paged.Items, nil
}

// eachByID fetches the full item of each listed one from prefix + its id.
func eachByID(ctx context.Context, c *client.Client, prefix string, listed []json.RawMessage) ([]json.RawMessage, error) {
	full := make([]json.RawMessage, 0, len(listed))
	for _, item := range listed {
		var row evidenceRow
		if err := json.Unmarshal(item, &row); err != nil || row.key() == "" {
			return nil, fmt.Errorf("GET %s: an item has no id", strings.TrimSuffix(prefix, "/"))
		}
		data, err := c.GetRaw(ctx, prefix+url.PathEscape(row.key()), nil)
		if err != nil {
			return nil, fmt.Errorf("GET %s%s: %w", prefix, row.key(), err)
		}
		full = append(full, data)
	}
	return full, nil
}

// auditInPeriod refuses rows outside [from, to): a Kates API that ignores
// until also reads only its newest 500 rows, so its list can't be evidence.
func auditInPeriod(rows []json.RawMessage, from, to time.Time) error {
	for _, item := range rows {
		var row struct {
			Timestamp string `json:"timestamp"`
		}
		_ = json.Unmarshal(item, &row)
		t, err := time.Parse(time.RFC3339Nano, row.Timestamp)
		if err != nil || t.Before(from) || !t.Before(to) {
			return fmt.Errorf("GET /api/audit answered a row from %q, outside the period: this Kates API pages its"+
				" audit log in memory and ignores until; upgrade it", row.Timestamp)
		}
	}
	return nil
}

// evidenceHint says which upgrade a 404 from an endpoint older Kates APIs lack calls for.
func evidenceHint(err error) string {
	var he *client.HTTPError
	if errors.As(err, &he) && he.StatusCode == http.StatusNotFound &&
		(strings.Contains(err.Error(), "/api/conformity/checks") || strings.Contains(err.Error(), "/api/audit/verify")) {
		return err.Error() + ". This Kates API stores no compliance checks or keeps no audit chain yet: upgrade it."
	}
	return err.Error()
}

func writeEvidence(dir string, pack *evidencePack) error {
	contents := map[string][]byte{"export.json": pack.export, "audit-verify.json": pack.verify}
	for name, items := range pack.lists {
		data, err := json.MarshalIndent(items, "", "  ")
		if err != nil {
			return err
		}
		contents[name] = data
	}
	var sums strings.Builder
	for _, name := range evidenceFiles {
		data := append(contents[name], '\n')
		if err := os.WriteFile(filepath.Join(dir, name), data, 0o640); err != nil {
			return err
		}
		sum := sha256.Sum256(data)
		fmt.Fprintf(&sums, "%s  %s\n", hex.EncodeToString(sum[:]), name)
	}
	return os.WriteFile(filepath.Join(dir, "SHA256SUMS"), []byte(sums.String()), 0o640)
}

// signEvidence runs cosign in the foreground: keyless signing prints a URL
// and waits for the browser sign-in, which captured output would hide.
func signEvidence(ctx context.Context, dir string) error {
	cosign, err := cosignPathFn()
	if err != nil {
		return fmt.Errorf("cosign is not on PATH: install it from https://docs.sigstore.dev")
	}
	c := exec.CommandContext(ctx, cosign, "sign-blob", "--yes",
		"--bundle", filepath.Join(dir, "SHA256SUMS.sigstore.json"), filepath.Join(dir, "SHA256SUMS"))
	c.Stdin, c.Stdout, c.Stderr = os.Stdin, os.Stderr, os.Stderr
	return c.Run()
}

func init() {
	evidenceExportCmd.Flags().StringVar(&evidenceFrom, "from", "", "Start of the period, an RFC 3339 time (required)")
	evidenceExportCmd.Flags().StringVar(&evidenceTo, "to", "", "End of the period, excluded, an RFC 3339 time (default now)")
	evidenceExportCmd.Flags().StringVar(&evidenceOut, "out", "", "Directory to create for the evidence (required)")
	evidenceExportCmd.Flags().BoolVar(&evidenceSign, "sign", false, "Sign SHA256SUMS with cosign, keyless")
	_ = evidenceExportCmd.MarkFlagRequired("from")
	_ = evidenceExportCmd.MarkFlagRequired("out")
	evidenceCmd.AddCommand(evidenceExportCmd)
	rootCmd.AddCommand(evidenceCmd)
}
