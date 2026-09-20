// Package report renders a compare.Result as a console summary, a JSON
// file, and/or a self-contained HTML file for manual review.
package report

import (
	"encoding/json"
	"fmt"
	"html/template"
	"io"
	"os"
	"path/filepath"

	"github.com/jyothri/bhandaar/agent/linux/internal/compare"
)

// WriteConsole prints a human-readable summary to w.
func WriteConsole(w io.Writer, res compare.Result) {
	fmt.Fprintf(w, "Drive comparison: %s vs %s\n", res.DriveA, res.DriveB)
	fmt.Fprintf(w, "  common:    %d\n", len(res.Common))
	fmt.Fprintf(w, "  diverged:  %d\n", len(res.Diverged))
	fmt.Fprintf(w, "  relocated: %d\n", len(res.Relocated))
	fmt.Fprintf(w, "  missing:   %d\n", len(res.Missing))
	if len(res.ScanErrors) > 0 {
		fmt.Fprintf(w, "  scan errors (unreadable during scan): %d\n", len(res.ScanErrors))
	}
	if res.ExcludedCount > 0 {
		fmt.Fprintf(w, "  excluded from report (macOS ._*/.DS_Store, still in checkpoint db): %d\n", res.ExcludedCount)
	}

	const previewN = 10
	if n := len(res.Diverged); n > 0 {
		fmt.Fprintf(w, "\nDiverged (same path, different content) — showing up to %d of %d:\n", min(previewN, n), n)
		for i, d := range res.Diverged {
			if i >= previewN {
				break
			}
			fmt.Fprintf(w, "  %s\n", d.RelPath)
		}
	}
	if n := len(res.Missing); n > 0 {
		fmt.Fprintf(w, "\nMissing from one drive — showing up to %d of %d:\n", min(previewN, n), n)
		for i, m := range res.Missing {
			if i >= previewN {
				break
			}
			fmt.Fprintf(w, "  %s (present on %s only)\n", m.RelPath, m.PresentOn)
		}
	}
}

// WriteJSON marshals the full result to path.
func WriteJSON(res compare.Result, path string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	enc := json.NewEncoder(f)
	enc.SetIndent("", "  ")
	return enc.Encode(res)
}

// htmlData is what the HTML template renders: the flat compare.Result
// (for the summary counts and the separate Relocated list) plus the
// derived folder-rollup tree (§7.1) built from everything except Relocated.
type htmlData struct {
	compare.Result
	Tree *TreeNode
}

// WriteHTML renders a single-file, static, browsable HTML report to path:
// a recursive, collapsed-by-default folder tree for Common/Diverged/
// Missing/ScanErrors (§7.1), plus a separate flat list for Relocated
// files, which don't have one natural folder home in the tree.
func WriteHTML(res compare.Result, path string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	return htmlTemplate.Execute(f, htmlData{Result: res, Tree: BuildTree(res)})
}

// humanSize formats a byte count as e.g. "1.3 MB", matching the binary
// (1024-based) convention `du -h` uses.
func humanSize(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for m := n / unit; m >= unit; m /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(n)/float64(div), "KMGTPE"[exp])
}

var htmlTemplate = template.Must(template.New("report").Funcs(template.FuncMap{
	"humanSize": humanSize,
}).Parse(`<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="utf-8">
<title>Drive comparison: {{.DriveA}} vs {{.DriveB}}</title>
<style>
  body { font-family: system-ui, sans-serif; margin: 2rem; color: #1a1a1a; }
  h1 { font-size: 1.3rem; }
  h2 { font-size: 1.05rem; margin-top: 2rem; }
  .summary { display: flex; gap: 1.5rem; margin-bottom: 1.5rem; flex-wrap: wrap; }
  .summary div { background: #f2f2f2; padding: 0.5rem 1rem; border-radius: 6px; }
  .tree details { margin-left: 1.1rem; }
  .tree > details { margin-left: 0; }
  .tree summary { cursor: pointer; padding: 0.15rem 0; list-style: revert; }
  .tree summary:hover { text-decoration: underline; }
  .badge { display: inline-block; font-size: 0.78em; padding: 0.05rem 0.5rem; border-radius: 999px; margin-left: 0.4rem; }
  .badge.cat-common { background: #e6f4ea; color: #1e7a34; }
  .badge.cat-diverged { background: #fde2e2; color: #a12b2b; }
  .badge.cat-missing { background: #e6ecfd; color: #2b47a1; }
  .badge.cat-error { background: #e6e6e6; color: #444; }
  .detail { margin: 0.2rem 0 0.3rem 1.1rem; font-size: 0.85rem; color: #444; }
  table { border-collapse: collapse; width: 100%; font-size: 0.9rem; margin-top: 0.5rem; }
  th, td { border-bottom: 1px solid #ddd; padding: 0.4rem 0.6rem; text-align: left; }
  th { background: #fafafa; }
  code { font-size: 0.85em; }
</style>
</head>
<body>
<h1>Drive comparison: {{.DriveA}} vs {{.DriveB}}</h1>
<div class="summary">
  <div>Common: {{len .Common}}</div>
  <div>Diverged: {{len .Diverged}}</div>
  <div>Relocated: {{len .Relocated}}</div>
  <div>Missing: {{len .Missing}}</div>
  <div>Scan errors: {{len .ScanErrors}}</div>
  {{if .ExcludedCount}}<div>Excluded (macOS metadata): {{.ExcludedCount}}</div>{{end}}
</div>

<h2>Files</h2>
<div class="tree">
{{template "node" .Tree}}
</div>

{{if .Relocated}}
<h2>Relocated ({{len .Relocated}})</h2>
<table>
<thead><tr><th>Path on {{.DriveA}}</th><th>Path on {{.DriveB}}</th><th>Size</th></tr></thead>
<tbody>
{{range .Relocated}}<tr><td><code>{{.PathA}}</code></td><td><code>{{.PathB}}</code></td><td>{{humanSize .Size}}</td></tr>
{{end}}
</tbody>
</table>
{{end}}

{{define "node"}}
{{if .IsFile}}
<details class="leaf">
  <summary>{{.Name}}<span class="badge cat-{{.Category}}">{{.Category}}</span></summary>
  <div class="detail">
    {{if eq .Category "diverged"}}size {{humanSize .SizeA}} on drive A vs {{humanSize .SizeB}} on drive B; hash {{.HashA}} vs {{.HashB}}
    {{else if eq .Category "missing"}}present on {{.PresentOn}} only ({{humanSize .Size}})
    {{else if eq .Category "error"}}scan error on {{.Drive}}: {{.Message}}
    {{else}}{{humanSize .Size}}
    {{end}}
  </div>
</details>
{{else}}
<details>
  <summary>{{.Name}}/<span class="badge cat-{{.Category}}">{{.Category}}{{with .CountsText}} ({{.}}){{end}}</span></summary>
  <div class="children">
    {{range .Children}}{{template "node" .}}{{end}}
  </div>
</details>
{{end}}
{{end}}
</body>
</html>
`))
