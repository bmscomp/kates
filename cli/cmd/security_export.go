package cmd

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/go-pdf/fpdf"
)

func exportAuditReport(result map[string]interface{}, filePath string) error {
	ext := filepath.Ext(filePath)
	if ext == ".json" {
		data, err := json.MarshalIndent(result, "", "  ")
		if err != nil {
			return err
		}
		return os.WriteFile(filePath, data, 0644)
	}

	if ext == ".pdf" {
		return exportAuditPDF(result, filePath)
	}

	grade := fmt.Sprintf("%v", result["grade"])
	summary, _ := result["summary"].(map[string]interface{})
	checks, _ := result["checks"].([]interface{})

	var sb strings.Builder
	if ext == ".md" {
		sb.WriteString("# Kafka Security Audit Report\n\n")
		sb.WriteString(fmt.Sprintf("**Grade: %s** | Generated: %v\n\n", grade, result["timestamp"]))
		if summary != nil {
			sb.WriteString(fmt.Sprintf("| Metric | Count |\n|--------|-------|\n"))
			sb.WriteString(fmt.Sprintf("| Total | %v |\n", summary["total"]))
			sb.WriteString(fmt.Sprintf("| Passed | %v |\n", summary["passed"]))
			sb.WriteString(fmt.Sprintf("| Warnings | %v |\n", summary["warnings"]))
			sb.WriteString(fmt.Sprintf("| Failures | %v |\n\n", summary["failures"]))
		}
		sb.WriteString("| Status | CIS | Check | Severity | Detail |\n")
		sb.WriteString("|--------|-----|-------|----------|--------|\n")
		for _, c := range checks {
			chk, ok := c.(map[string]interface{})
			if !ok {
				continue
			}
			status := fmt.Sprintf("%v", chk["status"])
			icon := "✓"
			if status == "FAIL" {
				icon = "✗"
			} else if status == "WARN" {
				icon = "⚠"
			}
			sb.WriteString(fmt.Sprintf("| %s | %v | %v | %v | %v |\n",
				icon, chk["compliance"], chk["name"], chk["severity"], chk["detail"]))
		}
		return os.WriteFile(filePath, []byte(sb.String()), 0644)
	}

	if ext == ".txt" {
		sb.WriteString(fmt.Sprintf("KAFKA SECURITY AUDIT REPORT\n"))
		sb.WriteString(fmt.Sprintf("Grade: %s\n", grade))
		sb.WriteString(fmt.Sprintf("Generated: %v\n\n", result["timestamp"]))
		if summary != nil {
			sb.WriteString(fmt.Sprintf("Total: %v  |  Passed: %v  |  Warnings: %v  |  Failures: %v\n\n",
				summary["total"], summary["passed"], summary["warnings"], summary["failures"]))
		}
		for _, c := range checks {
			chk, ok := c.(map[string]interface{})
			if !ok {
				continue
			}
			status := fmt.Sprintf("%v", chk["status"])
			icon := "[PASS]"
			if status == "FAIL" {
				icon = "[FAIL]"
			} else if status == "WARN" {
				icon = "[WARN]"
			}
			sb.WriteString(fmt.Sprintf("  %s  %-8v  %-28v  %-8v  %v\n",
				icon, chk["compliance"], chk["name"], chk["severity"], chk["detail"]))
		}
		sb.WriteString("\nREMEDIATION\n")
		for _, c := range checks {
			chk, ok := c.(map[string]interface{})
			if !ok {
				continue
			}
			if fmt.Sprintf("%v", chk["status"]) != "PASS" {
				sb.WriteString(fmt.Sprintf("  - %v: %v\n", chk["name"], chk["fix"]))
			}
		}
		return os.WriteFile(filePath, []byte(sb.String()), 0644)
	}
	sb.WriteString(`<!DOCTYPE html><html><head><meta charset="utf-8">
<title>Kafka Security Audit</title>
<style>
body{font-family:'Segoe UI',system-ui,sans-serif;background:#0f172a;color:#e2e8f0;margin:0;padding:2rem}
.container{max-width:1100px;margin:0 auto}
h1{color:#c4b5fd;border-bottom:2px solid #7c3aed;padding-bottom:.5rem}
.grade{font-size:3rem;font-weight:bold;text-align:center;padding:1rem;border-radius:12px;margin:1rem 0}
.grade-a,.grade-b{background:#065f46;color:#10b981}
.grade-c{background:#78350f;color:#f59e0b}
.grade-d,.grade-f{background:#7f1d1d;color:#ef4444}
.summary{display:flex;gap:1rem;margin:1rem 0}
.card{background:#1e293b;border-radius:8px;padding:1rem 1.5rem;flex:1;text-align:center}
.card h3{color:#94a3b8;margin:0 0 .5rem 0;font-size:.85rem;text-transform:uppercase}
.card .num{font-size:2rem;font-weight:bold}
.pass{color:#10b981}.warn{color:#f59e0b}.fail{color:#ef4444}
table{width:100%;border-collapse:collapse;margin:1rem 0}
th{background:#1e293b;color:#c4b5fd;padding:.6rem;text-align:left;font-size:.8rem;text-transform:uppercase}
td{padding:.5rem .6rem;border-bottom:1px solid #334155;font-size:.9rem}
tr:hover{background:#1e293b}
.status-pass{color:#10b981}.status-warn{color:#f59e0b}.status-fail{color:#ef4444}
footer{text-align:center;color:#64748b;margin-top:2rem;font-size:.8rem}
</style></head><body><div class="container">
`)

	sb.WriteString(fmt.Sprintf("<h1>Kafka Security Audit Report</h1>\n"))
	gradeClass := "grade-" + strings.ToLower(grade)
	sb.WriteString(fmt.Sprintf(`<div class="grade %s">Grade: %s</div>`, gradeClass, grade))

	if summary != nil {
		sb.WriteString(`<div class="summary">`)
		sb.WriteString(fmt.Sprintf(`<div class="card"><h3>Total</h3><div class="num">%v</div></div>`, summary["total"]))
		sb.WriteString(fmt.Sprintf(`<div class="card"><h3>Passed</h3><div class="num pass">%v</div></div>`, summary["passed"]))
		sb.WriteString(fmt.Sprintf(`<div class="card"><h3>Warnings</h3><div class="num warn">%v</div></div>`, summary["warnings"]))
		sb.WriteString(fmt.Sprintf(`<div class="card"><h3>Failures</h3><div class="num fail">%v</div></div>`, summary["failures"]))
		sb.WriteString(`</div>`)
	}

	sb.WriteString(`<table><thead><tr><th>Status</th><th>CIS</th><th>Check</th><th>Severity</th><th>Detail</th><th>Remediation</th></tr></thead><tbody>`)
	for _, c := range checks {
		chk, ok := c.(map[string]interface{})
		if !ok {
			continue
		}
		status := fmt.Sprintf("%v", chk["status"])
		statusClass := "status-pass"
		icon := "✓"
		if status == "FAIL" {
			statusClass = "status-fail"
			icon = "✗"
		} else if status == "WARN" {
			statusClass = "status-warn"
			icon = "⚠"
		}
		sb.WriteString(fmt.Sprintf(`<tr><td class="%s">%s</td><td>%v</td><td>%v</td><td>%v</td><td>%v</td><td>%v</td></tr>`,
			statusClass, icon, chk["compliance"], chk["name"], chk["severity"], chk["detail"], chk["fix"]))
	}
	sb.WriteString("</tbody></table>\n")
	sb.WriteString(fmt.Sprintf(`<footer>Generated by kates security audit — %v</footer>`, result["timestamp"]))
	sb.WriteString("</div></body></html>")

	return os.WriteFile(filePath, []byte(sb.String()), 0644)
}

func exportAuditPDF(result map[string]interface{}, filePath string) error {
	pdf := fpdf.New("L", "mm", "A4", "")
	pdf.SetAutoPageBreak(true, 15)
	pdf.AddPage()

	grade := fmt.Sprintf("%v", result["grade"])
	summary, _ := result["summary"].(map[string]interface{})
	checks, _ := result["checks"].([]interface{})

	pdf.SetFont("Helvetica", "B", 22)
	pdf.SetTextColor(100, 80, 200)
	pdf.CellFormat(0, 12, "Kafka Security Audit Report", "", 1, "L", false, 0, "")
	pdf.SetFont("Helvetica", "", 9)
	pdf.SetTextColor(120, 120, 120)
	pdf.CellFormat(0, 6, fmt.Sprintf("Generated: %v", result["timestamp"]), "", 1, "L", false, 0, "")
	pdf.Ln(4)

	pdf.SetFont("Helvetica", "B", 36)
	switch grade {
	case "A", "B":
		pdf.SetTextColor(16, 185, 129)
	case "C":
		pdf.SetTextColor(245, 158, 11)
	default:
		pdf.SetTextColor(239, 68, 68)
	}
	pdf.CellFormat(40, 20, "Grade: "+grade, "", 1, "L", false, 0, "")
	pdf.Ln(2)

	if summary != nil {
		pdf.SetFont("Helvetica", "", 11)
		pdf.SetTextColor(60, 60, 60)
		pdf.CellFormat(0, 7, fmt.Sprintf("Total: %v   |   Passed: %v   |   Warnings: %v   |   Failures: %v",
			summary["total"], summary["passed"], summary["warnings"], summary["failures"]), "", 1, "L", false, 0, "")
		pdf.Ln(4)
	}

	colWidths := []float64{10, 20, 55, 20, 170}
	headers := []string{"", "CIS", "Check", "Severity", "Detail"}
	pdf.SetFont("Helvetica", "B", 8)
	pdf.SetFillColor(30, 41, 59)
	pdf.SetTextColor(196, 181, 253)
	for i, h := range headers {
		pdf.CellFormat(colWidths[i], 7, h, "1", 0, "L", true, 0, "")
	}
	pdf.Ln(-1)

	pdf.SetFont("Helvetica", "", 8)
	for _, c := range checks {
		chk, ok := c.(map[string]interface{})
		if !ok {
			continue
		}
		status := fmt.Sprintf("%v", chk["status"])
		icon := "OK"
		if status == "FAIL" {
			icon = "FAIL"
			pdf.SetTextColor(239, 68, 68)
		} else if status == "WARN" {
			icon = "WARN"
			pdf.SetTextColor(245, 158, 11)
		} else {
			pdf.SetTextColor(16, 185, 129)
		}
		pdf.CellFormat(colWidths[0], 6, icon, "1", 0, "C", false, 0, "")
		pdf.SetTextColor(60, 60, 60)
		pdf.CellFormat(colWidths[1], 6, fmt.Sprintf("%v", chk["compliance"]), "1", 0, "L", false, 0, "")
		pdf.CellFormat(colWidths[2], 6, fmt.Sprintf("%v", chk["name"]), "1", 0, "L", false, 0, "")
		pdf.CellFormat(colWidths[3], 6, fmt.Sprintf("%v", chk["severity"]), "1", 0, "L", false, 0, "")
		detail := fmt.Sprintf("%v", chk["detail"])
		if len(detail) > 95 {
			detail = detail[:94] + "..."
		}
		pdf.CellFormat(colWidths[4], 6, detail, "1", 0, "L", false, 0, "")
		pdf.Ln(-1)
	}

	pdf.Ln(6)
	pdf.SetFont("Helvetica", "B", 12)
	pdf.SetTextColor(100, 80, 200)
	pdf.CellFormat(0, 8, "Remediation", "", 1, "L", false, 0, "")
	pdf.SetFont("Helvetica", "", 9)
	pdf.SetTextColor(60, 60, 60)
	for _, c := range checks {
		chk, ok := c.(map[string]interface{})
		if !ok {
			continue
		}
		if fmt.Sprintf("%v", chk["status"]) != "PASS" {
			pdf.CellFormat(0, 5, fmt.Sprintf("- %v: %v", chk["name"], chk["fix"]), "", 1, "L", false, 0, "")
		}
	}

	return pdf.OutputFileAndClose(filePath)
}
