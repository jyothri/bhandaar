// Package report renders already-computed comparison data as a console
// summary, a JSON file, and/or a self-contained HTML file. It performs no
// comparison computation of its own — everything here is a pure read of
// the checkpoint DB (folder_status / dir_listings / files), so it needs no
// drive to be mounted and can be re-run any number of times for free. See
// specs/drive-comparison-agent.md.
package report

import (
	"encoding/json"
	"fmt"
	"html/template"
	"io"
	"os"
	"path/filepath"
	"strings"

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

// htmlDriveData is what the HTML template renders per drive: the
// relocated-files table (rendered as before) plus the tree data, embedded
// as inert <script type="application/json"> blocks and rendered lazily by
// client-side JS rather than expanded into HTML server-side. This keeps
// the report responsive even at millions of files, since the browser only
// ever builds DOM (and only ever parses JSON) for whatever subtree the
// user actually expands.
type htmlDriveData struct {
	DriveID        string
	RelocatedCount int
	DataScripts    template.HTML
}

// jsonScriptTag marshals v and wraps it in an inert (non-executing)
// <script type="application/json" id="id"> block. encoding/json escapes
// '<', '>' and '&' by default, so the output can never contain a literal
// "</script>" sequence — safe to embed verbatim.
func jsonScriptTag(id string, v any) (string, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return "", err
	}
	var sb strings.Builder
	sb.WriteString(`<script type="application/json" id="`)
	sb.WriteString(template.HTMLEscapeString(id))
	sb.WriteString(`">`)
	sb.Write(b)
	sb.WriteString(`</script>`)
	return sb.String(), nil
}

// writeChunkFile writes one lazily-loaded subtree as its own small JS
// file (report_data/<id>.js) rather than embedding it in report.html.
// Measured empirically: even inert, non-executing data embedded directly
// in the HTML forces the browser to load the whole file into memory
// before anything renders — a report covering hundreds of thousands of
// files stays a ~150MB+ single document and hangs on open regardless of
// how lazily its DOM is built. Splitting chunks into sidecar files loaded
// via a dynamically-inserted <script src> (fetched only when the user
// expands into that subtree) keeps the initial document tiny; <script
// src> to a same-directory file works under file:// without a server,
// unlike fetch()/XHR, which Chrome blocks there.
func writeChunkFile(dataDir string, ch htmlChunk) error {
	node, err := json.Marshal(ch.Node)
	if err != nil {
		return err
	}
	id, err := json.Marshal(ch.ID)
	if err != nil {
		return err
	}
	var sb strings.Builder
	sb.WriteString("__registerChunk(")
	sb.Write(id)
	sb.WriteString(",")
	sb.Write(node)
	sb.WriteString(");")
	return os.WriteFile(filepath.Join(dataDir, ch.ID+".js"), []byte(sb.String()), 0o644)
}

// WriteHTML renders a static, browsable HTML report to path, one
// Overview + recursive tree per drive, selectable via tabs. Each drive's
// root is embedded directly (always small); everything below it is
// rendered lazily in the browser from sidecar files under a report_data/
// directory next to path (see writeChunkFile), so opening the report
// stays fast regardless of how many files it covers.
func WriteHTML(reports []DriveReport, path string) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	dataDir := filepath.Join(dir, "report_data")
	if err := os.RemoveAll(dataDir); err != nil {
		return fmt.Errorf("clearing stale %s: %w", dataDir, err)
	}
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		return err
	}

	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()

	data := struct{ Reports []htmlDriveData }{}
	for _, r := range reports {
		root, chunks := exportHTMLTree(r.DriveID, r.Tree)

		rootTag, err := jsonScriptTag("root-"+r.DriveID, root)
		if err != nil {
			return fmt.Errorf("marshaling tree for %q: %w", r.DriveID, err)
		}
		for _, ch := range chunks {
			if err := writeChunkFile(dataDir, ch); err != nil {
				return fmt.Errorf("writing chunk %q for %q: %w", ch.ID, r.DriveID, err)
			}
		}

		// Relocated entries are a flat list (no hierarchy to lazily
		// expand into), so — unlike the tree — they're embedded directly
		// and paginated client-side rather than split into sidecar files:
		// a report's relocated list, even in the tens of thousands, is
		// small enough in JSON to embed and parse cheaply; it's rendering
		// all of it as <tr> elements at once that made the page heavy.
		relocatedTag, err := jsonScriptTag("relocated-"+r.DriveID, r.Relocated)
		if err != nil {
			return fmt.Errorf("marshaling relocated entries for %q: %w", r.DriveID, err)
		}

		data.Reports = append(data.Reports, htmlDriveData{
			DriveID: r.DriveID, RelocatedCount: len(r.Relocated),
			DataScripts: template.HTML(rootTag + relocatedTag),
		})
	}
	return htmlTemplate.Execute(f, data)
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
  .dirsize { font-size: 0.78em; margin-left: 0.4rem; }
  .dirsize.partial { color: #999; }
  .dirsize.full { color: #1a1a1a; }
  .detail { margin: 0.2rem 0 0.3rem 1.1rem; font-size: 0.85rem; color: #444; }
  table { border-collapse: collapse; width: 100%; font-size: 0.9rem; margin-top: 0.5rem; }
  th, td { border-bottom: 1px solid #ddd; padding: 0.4rem 0.6rem; text-align: left; }
  th { background: #fafafa; }
  code { font-size: 0.85em; }
  .pager { display: flex; align-items: center; gap: 0.75rem; margin-top: 0.5rem; font-size: 0.85rem; color: #444; }
  .pager button { padding: 0.25rem 0.7rem; cursor: pointer; }
  .pager button:disabled { cursor: default; opacity: 0.5; }
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
<div class="tree" id="tree-{{$r.DriveID}}"></div>
{{$r.DataScripts}}

{{if $r.RelocatedCount}}
<h2>Relocated ({{$r.RelocatedCount}})</h2>
<table>
<thead><tr><th>Path on {{$r.DriveID}}</th><th>Relocated to</th><th>Size</th></tr></thead>
<tbody id="relocated-body-{{$r.DriveID}}"></tbody>
</table>
<div class="pager" id="relocated-pager-{{$r.DriveID}}"></div>
{{end}}
</div>
{{end}}

<script>
  function showDrive(id) {
    document.querySelectorAll('.tabs button').forEach(b => b.classList.toggle('active', b.dataset.drive === id));
    document.querySelectorAll('.drive-panel').forEach(p => p.classList.toggle('active', p.id === 'panel-' + id));
  }

  // Lazy tree rendering: the full tree is never embedded in this file at
  // all. Each drive's root is a small inline <script type=application/json>;
  // any subtree too big to inline lives in its own report_data/<id>.js
  // file (see html_export.go / report.go) and is only loaded — via a
  // dynamically-inserted <script src>, not fetch() (blocked under
  // file://) — the first time the user expands into it.
  const chunkCache = {};
  const chunkPromises = {};
  window.__registerChunk = function (id, node) { chunkCache[id] = node.ch || []; };

  function loadChunkChildren(id) {
    if (id in chunkCache) return Promise.resolve(chunkCache[id]);
    if (id in chunkPromises) return chunkPromises[id];
    chunkPromises[id] = new Promise((resolve, reject) => {
      const s = document.createElement('script');
      s.src = 'report_data/' + id + '.js';
      s.onload = () => resolve(chunkCache[id] || []);
      s.onerror = () => reject(new Error('failed to load ' + s.src));
      document.body.appendChild(s);
    });
    return chunkPromises[id];
  }

  const UNITS = ['B','KB','MB','GB','TB','PB','EB'];
  function humanSize(n) {
    if (n < 1024) return n + ' B';
    let div = 1024, exp = 0;
    for (let m = Math.floor(n / 1024); m >= 1024; m = Math.floor(m / 1024)) { div *= 1024; exp++; }
    return (n / div).toFixed(1) + ' ' + UNITS[exp + 1];
  }

  const COUNT_ORDER = ['diverged', 'relocated', 'missing', 'common'];
  function badge(node) {
    const span = document.createElement('span');
    span.className = 'badge ' + node.c;
    let text = node.c;
    if (!node.f && node.c !== 'common' && node.cnt) {
      const parts = [];
      for (const k of COUNT_ORDER) {
        if (node.cnt[k]) parts.push(node.cnt[k] + ' ' + k);
      }
      if (parts.length) text += ' (' + parts.join(', ') + ')';
    }
    span.textContent = text;
    return span;
  }

  function renderLeaf(node) {
    const details = document.createElement('details');
    details.className = 'leaf';
    const summary = document.createElement('summary');
    summary.appendChild(document.createTextNode(node.n));
    summary.appendChild(badge(node));
    details.appendChild(summary);
    const detail = document.createElement('div');
    detail.className = 'detail';
    if (node.c === 'relocated') {
      detail.textContent = 'relocated to ' + node.d + ': ' + node.r + ' (' + humanSize(node.s) + ')';
    } else if (node.c === 'unscanned') {
      detail.textContent = 'not yet compared';
    } else {
      detail.textContent = humanSize(node.s) + (node.d ? ' — vs ' + node.d : '');
    }
    details.appendChild(detail);
    return details;
  }

  function renderFolder(node) {
    const details = document.createElement('details');
    const summary = document.createElement('summary');
    summary.appendChild(document.createTextNode(node.n + '/'));
    summary.appendChild(badge(node));
    const sizeSpan = document.createElement('span');
    sizeSpan.className = 'dirsize ' + (node.p ? 'partial' : 'full');
    sizeSpan.textContent = humanSize(node.s);
    summary.appendChild(sizeSpan);
    details.appendChild(summary);
    const childrenDiv = document.createElement('div');
    childrenDiv.className = 'children';
    details.appendChild(childrenDiv);
    let rendered = false;
    details.addEventListener('toggle', () => {
      if (!details.open || rendered) return;
      rendered = true;
      if (node.ch) {
        for (const child of node.ch) childrenDiv.appendChild(renderNode(child));
        return;
      }
      if (!node.x) return;
      const loading = document.createElement('div');
      loading.className = 'detail';
      loading.textContent = 'Loading…';
      childrenDiv.appendChild(loading);
      loadChunkChildren(node.x).then(kids => {
        childrenDiv.removeChild(loading);
        for (const child of kids) childrenDiv.appendChild(renderNode(child));
      }).catch(err => {
        loading.textContent = err.message;
      });
    });
    return details;
  }

  function renderNode(node) {
    return node.f ? renderLeaf(node) : renderFolder(node);
  }

  function initTree(driveId) {
    const container = document.getElementById('tree-' + driveId);
    const root = JSON.parse(document.getElementById('root-' + driveId).textContent);
    container.appendChild(renderNode(root));
  }

  // Relocated table: paginated client-side rather than rendered as one
  // giant <table> — a report can have tens of thousands of relocated
  // entries, and building that many <tr> elements up front is exactly
  // the kind of bulk-DOM cost the tree's lazy rendering above avoids.
  const RELOCATED_PAGE_SIZE = 200;
  function initRelocatedTable(driveId) {
    const script = document.getElementById('relocated-' + driveId);
    if (!script) return;
    const entries = JSON.parse(script.textContent);
    script.remove();
    if (!entries.length) return;
    const body = document.getElementById('relocated-body-' + driveId);
    const pager = document.getElementById('relocated-pager-' + driveId);
    const totalPages = Math.ceil(entries.length / RELOCATED_PAGE_SIZE);
    let page = 0;

    function renderPage() {
      body.textContent = '';
      const start = page * RELOCATED_PAGE_SIZE;
      const pageEntries = entries.slice(start, start + RELOCATED_PAGE_SIZE);
      for (const e of pageEntries) {
        const tr = document.createElement('tr');
        const pathTd = document.createElement('td');
        const pathCode = document.createElement('code');
        pathCode.textContent = e.RelPath;
        pathTd.appendChild(pathCode);
        const toTd = document.createElement('td');
        toTd.appendChild(document.createTextNode(e.CounterpartDriveID + ': '));
        const toCode = document.createElement('code');
        toCode.textContent = e.CounterpartRelPath;
        toTd.appendChild(toCode);
        const sizeTd = document.createElement('td');
        sizeTd.textContent = humanSize(e.Size);
        tr.append(pathTd, toTd, sizeTd);
        body.appendChild(tr);
      }
      pager.textContent = '';
      const prev = document.createElement('button');
      prev.textContent = 'Prev';
      prev.disabled = page === 0;
      prev.onclick = () => { page--; renderPage(); };
      const next = document.createElement('button');
      next.textContent = 'Next';
      next.disabled = page >= totalPages - 1;
      next.onclick = () => { page++; renderPage(); };
      const label = document.createElement('span');
      label.textContent = 'Page ' + (page + 1) + ' of ' + totalPages + ' (' + entries.length + ' total)';
      pager.append(prev, next, label);
    }
    renderPage();
  }

  {{range .Reports}}initTree("{{.DriveID}}");
  initRelocatedTable("{{.DriveID}}");
  {{end}}
</script>
</body>
</html>
`))
