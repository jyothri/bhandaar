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

// WriteHTML renders a single-file, static, browsable HTML report to path.
func WriteHTML(res compare.Result, path string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	return htmlTemplate.Execute(f, res)
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
  .summary { display: flex; gap: 1.5rem; margin-bottom: 1.5rem; flex-wrap: wrap; }
  .summary div { background: #f2f2f2; padding: 0.5rem 1rem; border-radius: 6px; }
  .controls { margin-bottom: 0.75rem; display: flex; gap: 0.75rem; align-items: center; }
  table { border-collapse: collapse; width: 100%; font-size: 0.9rem; }
  th, td { border-bottom: 1px solid #ddd; padding: 0.4rem 0.6rem; text-align: left; }
  th { cursor: pointer; background: #fafafa; position: sticky; top: 0; }
  tr.cat-common { }
  tr.cat-diverged { background: #fff3f3; }
  tr.cat-relocated { background: #fff9e6; }
  tr.cat-missing { background: #f3f6ff; }
  tr.cat-error { background: #f0f0f0; }
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
</div>
<div class="controls">
  <label>Category:
    <select id="catFilter">
      <option value="">All</option>
      <option value="diverged">Diverged</option>
      <option value="missing">Missing</option>
      <option value="relocated">Relocated</option>
      <option value="common">Common</option>
      <option value="error">Scan errors</option>
    </select>
  </label>
  <input id="pathFilter" type="search" placeholder="Filter by path...">
</div>
<table id="report">
<thead><tr><th data-key="category">Category</th><th data-key="path">Path</th><th data-key="detail">Detail</th></tr></thead>
<tbody>
{{range .Diverged}}<tr class="cat-diverged"><td>diverged</td><td><code>{{.RelPath}}</code></td><td>size {{humanSize .SizeA}} vs {{humanSize .SizeB}}; hash {{.HashA}} vs {{.HashB}}</td></tr>
{{end}}
{{range .Missing}}<tr class="cat-missing"><td>missing</td><td><code>{{.RelPath}}</code></td><td>present on {{.PresentOn}} only ({{humanSize .Size}})</td></tr>
{{end}}
{{range .Relocated}}<tr class="cat-relocated"><td>relocated</td><td><code>{{.PathA}}</code> &harr; <code>{{.PathB}}</code></td><td>{{humanSize .Size}}</td></tr>
{{end}}
{{range .ScanErrors}}<tr class="cat-error"><td>scan error</td><td><code>{{.RelPath}}</code></td><td>[{{.Drive}}] {{.Message}}</td></tr>
{{end}}
{{range .Common}}<tr class="cat-common"><td>common</td><td><code>{{.RelPath}}</code></td><td>{{humanSize .Size}}</td></tr>
{{end}}
</tbody>
</table>
<script>
  const catFilter = document.getElementById('catFilter');
  const pathFilter = document.getElementById('pathFilter');
  const rows = Array.from(document.querySelectorAll('#report tbody tr'));

  function applyFilters() {
    const cat = catFilter.value;
    const needle = pathFilter.value.toLowerCase();
    for (const row of rows) {
      const rowCat = row.cells[0].textContent.trim().replace(' ', '-');
      const path = row.cells[1].textContent.toLowerCase();
      const catOk = !cat || row.className === 'cat-' + cat;
      const pathOk = !needle || path.includes(needle);
      row.style.display = (catOk && pathOk) ? '' : 'none';
    }
  }
  catFilter.addEventListener('change', applyFilters);
  pathFilter.addEventListener('input', applyFilters);

  document.querySelectorAll('#report th').forEach((th, idx) => {
    th.addEventListener('click', () => {
      const tbody = document.querySelector('#report tbody');
      const sorted = rows.slice().sort((a, b) =>
        a.cells[idx].textContent.localeCompare(b.cells[idx].textContent));
      sorted.forEach(r => tbody.appendChild(r));
    });
  });
</script>
</body>
</html>
`))
