package cmd

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/bmscomp/kates/cli/pkg/migrate"
)

// Where kates migrate's charts, pins and files come from: a checkout's, as
// before, or a release build's own. A release build pulls legacy-kafka and
// mirror-maker2 the way deploy pulls its charts (deploy_charts.go), takes
// the versions.env pins release.yml carried in, builds legacy images from
// the Dockerfile built into it, and keeps its generated files under the user
// state directory.

// MigratePins is "KEY=value,…": the versions.env pins kates migrate reads, at
// this build's tag; release.yml sets it, and a dev build reads versions.env.
var MigratePins = ""

// migrateChartDirs are the charts kates migrate installs, and the operator
// chart whose window it plans against when the cluster runs no operator.
var migrateChartDirs = []string{"legacy-kafka", "mirror-maker2", "strimzi-operator"}

// migrateChartsAndPins resolves the charts kates migrate installs and the
// pins it reads: a checkout's, with its versions.env, or this release's
// published charts and the pins it carries.
func migrateChartsAndPins(ctx context.Context) (migrate.Pins, error) {
	if err := resolveChartRoot(ctx, "", false, migrateChartDirs...); err != nil {
		return migrate.Pins{}, err
	}
	if chartsPublished {
		if MigratePins == "" {
			return migrate.Pins{}, fmt.Errorf("this kates build (%s) carries no versions.env pins; run kates migrate from a Kates checkout, or set KATES_CHECKOUT to one", Version)
		}
		return migrate.ParsePins(strings.ReplaceAll(MigratePins, ",", "\n"))
	}
	for _, p := range []string{
		filepath.Join(migrate.MirrorChartPath, "Chart.yaml"),
		filepath.Join(migrate.SourceChartPath, "Chart.yaml"),
		migrate.PinsFile,
	} {
		if _, err := os.Stat(chartRootPath(p)); err != nil {
			return migrate.Pins{}, fmt.Errorf("%s is not readable from here.\n"+
				"  kates migrate needs the charts and versions.env of a Kates checkout: run it from the repo root or set KATES_CHECKOUT, or use a release build of kates, which pulls the published charts", chartRootPath(p))
		}
	}
	return migrate.ReadPins(chartRoot)
}

// migrateBuildRoot is where the labs' generated values files live, one
// directory per lab: .build/migrate in a checkout, the working directory, as
// always; anywhere else kates/migrate under the user state directory
// ($XDG_STATE_HOME, else ~/.local/state), so a run from $HOME leaves nothing
// there. It depends on the working directory alone, so every command of a
// lab finds the same files.
func migrateBuildRoot() string {
	if _, err := os.Stat(filepath.Join(migrate.SourceChartPath, "Chart.yaml")); err == nil {
		return migrateBuildDir
	}
	state := os.Getenv("XDG_STATE_HOME")
	if state == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return migrateBuildDir
		}
		state = filepath.Join(home, ".local", "state")
	}
	return filepath.Join(state, "kates", "migrate")
}

// legacyDockerfile is the Dockerfile and build context of the legacy Kafka
// image: a checkout's own, or a temporary copy of the one built into this
// binary, which the returned function removes.
func legacyDockerfile() (dockerfile, buildContext string, remove func(), err error) {
	if path := chartRootPath(migrate.LegacyDockerfileName); fileExistsAt(path) {
		return path, chartRoot, func() {}, nil
	}
	dir, err := os.MkdirTemp("", "kates-legacy-kafka-")
	if err != nil {
		return "", "", nil, fmt.Errorf("writing the legacy Kafka Dockerfile: %w", err)
	}
	remove = func() { _ = os.RemoveAll(dir) }
	path := filepath.Join(dir, migrate.LegacyDockerfileName)
	if err := os.WriteFile(path, migrate.LegacyDockerfile, 0o644); err != nil {
		remove()
		return "", "", nil, errors.Join(errors.New("writing the legacy Kafka Dockerfile"), err)
	}
	return path, dir, remove, nil
}

func fileExistsAt(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}
