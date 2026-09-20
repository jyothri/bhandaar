// Package report renders already-computed comparison data as a console
// summary, a JSON file, and/or a self-contained HTML file. It performs no
// comparison computation of its own — everything here is a pure read of
// the checkpoint DB (folder_status / dir_listings / files), so it needs no
// drive to be mounted and can be re-run any number of times for free. See
// specs/drive-comparison-agent.md §11.6.
package report

import (
	"encoding/json"
	"fmt"
	"html/template"
	"io"
	"os"
	"path/filepath"

	"github.com/jyothri/bhandaar/agent/linux/internal/store"
)

// RelocatedEntry is one file relocated relative to its counterpart drive.
type RelocatedEntry struct {
	RelPath            string
	CounterpartDriveID string
	CounterpartRelPath string
	Size               int64
}

// DriveReport is everything rendered for one drive: its full folder/file
// tree (rooted at "."), and the files relocated relative to whichever
// drive(s) it's been compared against.
type DriveReport struct {
	DriveID   string
	Tree      *TreeNode
	Relocated []RelocatedEntry
}

// Options controls report generation.
type Options struct {
	Drives             []string
	IncludeMacMetadata bool
}

// Build reads the checkpoint DB and assembles one DriveReport per
// requested drive. No drive needs to be mounted.
func Build(st *store.Store, opts Options) ([]DriveReport, error) {
	reports := make([]DriveReport, 0, len(opts.Drives))
	for _, driveID := range opts.Drives {
		tree, err := buildTree(st, driveID, opts.IncludeMacMetadata)
		if err != nil {
			return nil, fmt.Errorf("building tree for %q: %w", driveID, err)
		}
		relocatedFiles, err := st.ListFilesByComparisonStatus(driveID, store.ComparisonRelocated)
		if err != nil {
			return nil, fmt.Errorf("listing relocated files for %q: %w", driveID, err)
		}
		relocated := make([]RelocatedEntry, 0, len(relocatedFiles))
		for _, f := range relocatedFiles {
			relocated = append(relocated, RelocatedEntry{
				RelPath: f.RelPath, Size: f.Size,
				CounterpartDriveID: f.ComparedAgainstDriveID,
				CounterpartRelPath: f.CounterpartRelativePath,
			})
		}
		reports = append(reports, DriveReport{DriveID: driveID, Tree: tree, Relocated: relocated})
	}
	return reports, nil
}

// WriteConsole prints a human-readable summary to w.
func WriteConsole(w io.Writer, reports []DriveReport) {
	for i, r := range reports {
		if i > 0 {
			fmt.Fprintln(w)
		}
		fmt.Fprintf(w, "Drive %s:\n", r.DriveID)
		fmt.Fprintf(w, "  %s%s\n", r.Tree.Category, ctextSuffix(r.Tree))
		if len(r.Relocated) > 0 {
			fmt.Fprintf(w, "  relocated: %d\n", len(r.Relocated))
		}
	}
}

func ctextSuffix(n *TreeNode) string {
	if t := n.CountsText(); t != "" {
		return " (" + t + ")"
	}
	return ""
}

// WriteJSON marshals every requested drive's report to path.
func WriteJSON(reports []DriveReport, path string) error {
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
	return enc.Encode(reports)
}

// WriteHTML renders a single-file, static, browsable HTML report to path,
// one Overview + recursive tree per drive, selectable via tabs.
func WriteHTML(reports []DriveReport, path string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	return htmlTemplate.Execute(f, struct{ Reports []DriveReport }{reports})
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
<title>Drive report</title>
<style>
  body { font-family: system-ui, sans-serif; margin: 2rem; color: #1a1a1a; }
  h1 { font-size: 1.3rem; }
  h2 { font-size: 1.05rem; margin-top: 2rem; }
  .tabs { display: flex; gap: 0.5rem; margin-bottom: 1rem; border-bottom: 1px solid #ddd; }
  .tabs button { border: none; background: none; padding: 0.5rem 1rem; cursor: pointer; font-size: 0.95rem; border-bottom: 2px solid transparent; }
  .tabs button.active { border-bottom-color: #333; font-weight: 600; }
  .drive-panel { display: none; }
  .drive-panel.active { display: block; }
  .tree details { margin-left: 1.1rem; }
  .tree > details { margin-left: 0; }
  .tree summary { cursor: pointer; padding: 0.15rem 0; list-style: revert; }
  .tree summary:hover { text-decoration: underline; }
  .badge { display: inline-block; font-size: 0.78em; padding: 0.05rem 0.5rem; border-radius: 999px; margin-left: 0.4rem; }
  .badge.common { background: #e6f4ea; color: #1e7a34; }
  .badge.diverged { background: #fde2e2; color: #a12b2b; }
  .badge.missing { background: #e6ecfd; color: #2b47a1; }
  .badge.relocated { background: #fff3cf; color: #8a6d00; }
  .badge.unscanned { background: #eee; color: #555; }
  .badge.partial { background: #f0e6fd; color: #5a2ba1; }
  .detail { margin: 0.2rem 0 0.3rem 1.1rem; font-size: 0.85rem; color: #444; }
  table { border-collapse: collapse; width: 100%; font-size: 0.9rem; margin-top: 0.5rem; }
  th, td { border-bottom: 1px solid #ddd; padding: 0.4rem 0.6rem; text-align: left; }
  th { background: #fafafa; }
  code { font-size: 0.85em; }
</style>
</head>
<body>
<h1>Drive report</h1>
<div class="tabs">
{{range $i, $r := .Reports}}<button data-drive="{{$r.DriveID}}" class="{{if eq $i 0}}active{{end}}" onclick="showDrive('{{$r.DriveID}}')">{{$r.DriveID}}</button>
{{end}}
</div>

{{range $i, $r := .Reports}}
<div class="drive-panel {{if eq $i 0}}active{{end}}" id="panel-{{$r.DriveID}}">
<h2>Overview</h2>
<div class="tree">
{{template "node" $r.Tree}}
</div>

{{if $r.Relocated}}
<h2>Relocated ({{len $r.Relocated}})</h2>
<table>
<thead><tr><th>Path on {{$r.DriveID}}</th><th>Relocated to</th><th>Size</th></tr></thead>
<tbody>
{{range $r.Relocated}}<tr><td><code>{{.RelPath}}</code></td><td>{{.CounterpartDriveID}}: <code>{{.CounterpartRelPath}}</code></td><td>{{humanSize .Size}}</td></tr>
{{end}}
</tbody>
</table>
{{end}}
</div>
{{end}}

{{define "node"}}
{{if .IsFile}}
<details class="leaf">
  <summary>{{.Name}}<span class="badge {{.Category}}">{{.Category}}</span></summary>
  <div class="detail">
    {{if eq .Category "relocated"}}relocated to {{.ComparedAgainstDriveID}}: {{.CounterpartRelativePath}} ({{humanSize .Size}})
    {{else if eq .Category "unscanned"}}not yet compared
    {{else}}{{humanSize .Size}}{{if .ComparedAgainstDriveID}} — vs {{.ComparedAgainstDriveID}}{{end}}
    {{end}}
  </div>
</details>
{{else}}
<details>
  <summary>{{.Name}}/<span class="badge {{.Category}}">{{.Category}}{{with .CountsText}} ({{.}}){{end}}</span></summary>
  <div class="children">
    {{range .Children}}{{template "node" .}}{{end}}
  </div>
</details>
{{end}}
{{end}}
<script>
  function showDrive(id) {
    document.querySelectorAll('.tabs button').forEach(b => b.classList.toggle('active', b.dataset.drive === id));
    document.querySelectorAll('.drive-panel').forEach(p => p.classList.toggle('active', p.id === 'panel-' + id));
  }
</script>
</body>
</html>
`))
