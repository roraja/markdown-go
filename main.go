package main

import (
	"encoding/json"
	"errors"
	"flag"
	"html/template"
	"io/fs"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

type app struct {
	root string
	tpl  *template.Template
}

type pageData struct {
	Root        string
	InitialFile string
}

func main() {
	rootFlag := flag.String("root", ".", "Root directory to scan for markdown files")
	portFlag := flag.String("port", "8080", "HTTP port to listen on")
	flag.Parse()

	absRoot, err := filepath.Abs(*rootFlag)
	if err != nil {
		log.Fatalf("resolve root: %v", err)
	}
	info, err := os.Stat(absRoot)
	if err != nil {
		log.Fatalf("stat root: %v", err)
	}
	if !info.IsDir() {
		log.Fatalf("root is not a directory: %s", absRoot)
	}

	tpl, err := template.New("index").Parse(indexHTML)
	if err != nil {
		log.Fatalf("parse template: %v", err)
	}

	a := &app{root: absRoot, tpl: tpl}
	mux := http.NewServeMux()
	mux.HandleFunc("/", a.handleIndex)
	mux.HandleFunc("/api/files", a.handleFiles)
	mux.HandleFunc("/api/file", a.handleFile)
	mux.HandleFunc("/api/health", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		_ = json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
	})

	addr := ":" + *portFlag
	log.Printf("Markdown viewer running on http://localhost%s (root: %s)", addr, absRoot)
	if err := http.ListenAndServe(addr, mux); err != nil {
		log.Fatal(err)
	}
}

func (a *app) handleIndex(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}

	initialFile := r.URL.Query().Get("file")
	if _, err := sanitizeRelativePath(initialFile); err != nil {
		initialFile = ""
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := a.tpl.Execute(w, pageData{
		Root:        a.root,
		InitialFile: initialFile,
	}); err != nil {
		http.Error(w, "template render failed", http.StatusInternalServerError)
	}
}

func (a *app) handleFiles(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	files, err := listMarkdownFiles(a.root)
	if err != nil {
		http.Error(w, "failed to list markdown files", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_ = json.NewEncoder(w).Encode(struct {
		Root  string   `json:"root"`
		Files []string `json:"files"`
	}{
		Root:  a.root,
		Files: files,
	})
}

func (a *app) handleFile(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	requested := r.URL.Query().Get("path")
	relPath, err := sanitizeRelativePath(requested)
	if err != nil {
		http.Error(w, "invalid path", http.StatusBadRequest)
		return
	}
	if !isMarkdownFile(relPath) {
		http.Error(w, "only markdown files are supported", http.StatusBadRequest)
		return
	}

	fullPath, err := secureJoin(a.root, relPath)
	if err != nil {
		http.Error(w, "invalid path", http.StatusBadRequest)
		return
	}

	content, err := os.ReadFile(fullPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			http.Error(w, "file not found", http.StatusNotFound)
			return
		}
		http.Error(w, "failed to read file", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_ = json.NewEncoder(w).Encode(struct {
		Path    string `json:"path"`
		Content string `json:"content"`
	}{
		Path:    relPath,
		Content: string(content),
	})
}

func listMarkdownFiles(root string) ([]string, error) {
	files := make([]string, 0, 16)

	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		if !isMarkdownFile(d.Name()) {
			return nil
		}

		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		files = append(files, filepath.ToSlash(rel))
		return nil
	})
	if err != nil {
		return nil, err
	}

	sort.Strings(files)
	return files, nil
}

func isMarkdownFile(path string) bool {
	ext := strings.ToLower(filepath.Ext(path))
	return ext == ".md" || ext == ".markdown"
}

func sanitizeRelativePath(path string) (string, error) {
	clean := strings.TrimSpace(path)
	if clean == "" {
		return "", errors.New("path is required")
	}
	clean = filepath.Clean(filepath.FromSlash(clean))
	if clean == "." || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) || filepath.IsAbs(clean) {
		return "", errors.New("invalid path")
	}
	return filepath.ToSlash(clean), nil
}

func secureJoin(root, rel string) (string, error) {
	joined := filepath.Join(root, filepath.FromSlash(rel))

	absRoot, err := filepath.Abs(root)
	if err != nil {
		return "", err
	}
	absJoined, err := filepath.Abs(joined)
	if err != nil {
		return "", err
	}

	relCheck, err := filepath.Rel(absRoot, absJoined)
	if err != nil {
		return "", err
	}
	if relCheck == ".." || strings.HasPrefix(relCheck, ".."+string(filepath.Separator)) {
		return "", errors.New("path escapes root")
	}
	return absJoined, nil
}

const indexHTML = `<!DOCTYPE html>
<html lang="en">
<head>
  <meta charset="utf-8" />
  <meta name="viewport" content="width=device-width, initial-scale=1" />
  <title>Markdown Viewer</title>
  <script src="https://cdn.jsdelivr.net/npm/marked/marked.min.js"></script>
  <script src="https://cdn.jsdelivr.net/npm/mermaid@11/dist/mermaid.min.js"></script>
  <style>
    :root {
      --bg: #0d1117;
      --panel: #161b22;
      --border: #30363d;
      --text: #c9d1d9;
      --muted: #8b949e;
      --link: #58a6ff;
      --code-bg: #161b22;
      --active: #1f6feb33;
      --sidebar-bg: #010409;
      --button-bg: #21262d;
      --button-hover: #30363d;
      --raw-code: #c9d1d9;
    }

    :root[data-theme="light"] {
      --bg: #f6f8fa;
      --panel: #ffffff;
      --border: #d0d7de;
      --text: #24292f;
      --muted: #57606a;
      --link: #0969da;
      --code-bg: #f6f8fa;
      --active: #0969da1a;
      --sidebar-bg: #ffffff;
      --button-bg: #f6f8fa;
      --button-hover: #eaeef2;
      --raw-code: #24292f;
    }

    * { box-sizing: border-box; }
    body {
      margin: 0;
      font-family: -apple-system, BlinkMacSystemFont, "Segoe UI", Helvetica, Arial, sans-serif;
      background: var(--bg);
      color: var(--text);
    }

    .app {
      display: grid;
      grid-template-columns: 300px minmax(0, 1fr);
      min-height: 100vh;
    }

    .app.sidebar-hidden {
      grid-template-columns: minmax(0, 1fr);
    }

    .app.sidebar-hidden .sidebar {
      display: none;
    }

    .sidebar {
      border-right: 1px solid var(--border);
      background: var(--sidebar-bg);
      padding: 16px;
      overflow-y: auto;
    }

    .sidebar h1 {
      margin: 0;
      font-size: 16px;
      font-weight: 700;
    }

    .root-path {
      margin-top: 6px;
      font-size: 12px;
      color: var(--muted);
      word-break: break-all;
    }

    .files {
      margin-top: 14px;
      display: flex;
      flex-direction: column;
      gap: 0;
      user-select: none;
    }

    .tree-item {
      display: flex;
      align-items: center;
      width: 100%;
      border: none;
      background: transparent;
      color: var(--text);
      text-align: left;
      padding: 3px 0;
      padding-right: 8px;
      cursor: pointer;
      font-size: 13px;
      font-family: inherit;
      line-height: 22px;
      height: 22px;
      white-space: nowrap;
      overflow: hidden;
      text-overflow: ellipsis;
      border-radius: 0;
    }

    .tree-item:hover { background: var(--panel); }
    .tree-item.active {
      background: var(--active);
    }

    .tree-chevron {
      display: inline-flex;
      align-items: center;
      justify-content: center;
      width: 16px;
      min-width: 16px;
      height: 22px;
      font-size: 10px;
      color: var(--muted);
      transition: transform 0.1s ease;
    }

    .tree-chevron.expanded {
      transform: rotate(90deg);
    }

    .tree-chevron.placeholder {
      visibility: hidden;
    }

    .tree-icon {
      display: inline-flex;
      align-items: center;
      justify-content: center;
      width: 16px;
      min-width: 16px;
      height: 22px;
      margin-right: 4px;
      font-size: 14px;
    }

    .tree-icon.folder-icon { color: #e8a64e; }
    .tree-icon.file-icon { color: var(--muted); }

    .tree-label {
      overflow: hidden;
      text-overflow: ellipsis;
    }

    .tree-children {
      display: flex;
      flex-direction: column;
    }

    .tree-children.collapsed {
      display: none;
    }

    .main {
      padding: 24px;
      overflow-y: auto;
    }

    .header {
      display: flex;
      align-items: center;
      justify-content: space-between;
      margin-bottom: 16px;
      gap: 12px;
    }

    .header-actions {
      display: flex;
      gap: 8px;
    }

    .header h2 {
      margin: 0;
      font-size: 20px;
      font-weight: 600;
      overflow-wrap: anywhere;
    }

    .muted {
      color: var(--muted);
      font-size: 13px;
      margin-top: 4px;
    }

    .btn {
      border: 1px solid var(--border);
      border-radius: 6px;
      background: var(--button-bg);
      color: var(--text);
      padding: 6px 12px;
      cursor: pointer;
      font-size: 13px;
    }

    .btn:hover { background: var(--button-hover); }

    .viewer {
      border: 1px solid var(--border);
      border-radius: 8px;
      background: var(--panel);
      padding: 24px;
    }

    .hidden { display: none; }

    .markdown-body {
      line-height: 1.7;
      color: var(--text);
    }
    .markdown-body h1, .markdown-body h2, .markdown-body h3 {
      border-bottom: 1px solid var(--border);
      padding-bottom: .3em;
      margin-top: 24px;
      margin-bottom: 16px;
    }
    .markdown-body h1 { font-size: 2em; }
    .markdown-body h2 { font-size: 1.5em; }
    .markdown-body h3 { font-size: 1.25em; }
    .markdown-body p, .markdown-body ul, .markdown-body ol { margin: 0 0 16px; }
    .markdown-body code {
      background: var(--code-bg);
      border: 1px solid var(--border);
      padding: 0.15em 0.35em;
      border-radius: 6px;
      font-family: ui-monospace, SFMono-Regular, Menlo, Monaco, Consolas, monospace;
      font-size: 85%;
    }
    .markdown-body pre {
      background: var(--code-bg);
      border: 1px solid var(--border);
      border-radius: 8px;
      padding: 12px;
      overflow-x: auto;
      margin-bottom: 16px;
    }
    .markdown-body pre code {
      border: 0;
      padding: 0;
      background: transparent;
    }
    .markdown-body blockquote {
      border-left: 4px solid #3b82f6;
      margin: 0 0 16px;
      padding: 0 12px;
      color: var(--muted);
    }
    .markdown-body table {
      border-collapse: collapse;
      width: 100%;
      margin-bottom: 16px;
      display: block;
      overflow-x: auto;
    }
    .markdown-body th, .markdown-body td {
      border: 1px solid var(--border);
      padding: 6px 10px;
      text-align: left;
    }
    .markdown-body a {
      color: var(--link);
      text-decoration: none;
    }
    .markdown-body a:hover { text-decoration: underline; }

    .mermaid-block {
      background: #fff;
      border-radius: 8px;
      padding: 16px;
      margin: 16px 0;
      overflow-x: auto;
    }

    #raw-code {
      color: var(--raw-code);
      white-space: pre-wrap;
      word-break: break-word;
      font-family: ui-monospace, SFMono-Regular, Menlo, Monaco, Consolas, monospace;
      font-size: 13px;
    }
  </style>
</head>
<body>
  <div class="app">
    <aside class="sidebar">
      <h1>Markdown Files</h1>
      <div class="root-path">{{ .Root }}</div>
      <div class="files" id="file-list">
        <div class="muted">Loading files…</div>
      </div>
    </aside>
    <main class="main">
      <div class="header">
        <div>
          <h2 id="file-name">Select a markdown file</h2>
          <div class="muted">GitHub-like markdown preview with Mermaid support</div>
        </div>
        <div class="header-actions">
          <button id="toggle-sidebar-btn" class="btn" type="button">Hide Sidebar</button>
          <button id="theme-toggle-btn" class="btn" type="button">Light Mode</button>
          <button id="toggle-raw-btn" class="btn hidden" type="button">Show Raw</button>
        </div>
      </div>
      <section class="viewer">
        <div id="rendered-content" class="markdown-body">
          <div class="muted">Pick a file from the left to render it.</div>
        </div>
        <div id="raw-content" class="hidden">
          <pre><code id="raw-code"></code></pre>
        </div>
      </section>
    </main>
  </div>

  <script>
    const INITIAL_FILE = {{ printf "%q" .InitialFile }};
    const appEl = document.querySelector('.app');
    const fileListEl = document.getElementById('file-list');
    const fileNameEl = document.getElementById('file-name');
    const renderedEl = document.getElementById('rendered-content');
    const rawContainerEl = document.getElementById('raw-content');
    const rawCodeEl = document.getElementById('raw-code');
    const toggleSidebarBtn = document.getElementById('toggle-sidebar-btn');
    const themeToggleBtn = document.getElementById('theme-toggle-btn');
    const toggleRawBtn = document.getElementById('toggle-raw-btn');
    const STORAGE_THEME_KEY = 'mdviewer-theme';

    let files = [];
    let activeFile = '';
    let rawContent = '';
    let showingRaw = false;
    let sidebarHidden = false;

    if (window.mermaid) {
      window.mermaid.initialize({ startOnLoad: false, securityLevel: 'loose', theme: 'neutral' });
    }

    loadThemePreference();

    toggleSidebarBtn.addEventListener('click', () => {
      applySidebarVisibility(!sidebarHidden, true);
    });

    themeToggleBtn.addEventListener('click', () => {
      const currentTheme = document.documentElement.getAttribute('data-theme') === 'light' ? 'light' : 'dark';
      const nextTheme = currentTheme === 'dark' ? 'light' : 'dark';
      applyTheme(nextTheme);
      window.localStorage.setItem(STORAGE_THEME_KEY, nextTheme);
    });

    toggleRawBtn.addEventListener('click', () => {
      showingRaw = !showingRaw;
      renderedEl.classList.toggle('hidden', showingRaw);
      rawContainerEl.classList.toggle('hidden', !showingRaw);
      toggleRawBtn.textContent = showingRaw ? 'Show Rendered' : 'Show Raw';
    });

    window.addEventListener('popstate', () => {
      const params = new URLSearchParams(window.location.search);
      applySidebarVisibility(isFullscreenMode(params), false);
      const candidate = params.get('file');
      if (candidate && files.includes(candidate)) {
        openFile(candidate, false);
      }
    });

    init();

    async function init() {
      try {
        const response = await fetch('/api/files');
        if (!response.ok) throw new Error('failed to list files');
        const payload = await response.json();
        files = payload.files || [];
        renderFileList();

        const params = new URLSearchParams(window.location.search);
        applySidebarVisibility(isFullscreenMode(params), false);
        const requested = params.get('file') || INITIAL_FILE;
        if (requested && files.includes(requested)) {
          await openFile(requested, false);
        } else if (files.length > 0) {
          await openFile(files[0], false);
        } else {
          renderedEl.innerHTML = '<div class="muted">No markdown files found in the configured root.</div>';
        }
      } catch (err) {
        renderedEl.innerHTML = '<div class="muted">Failed to load markdown files.</div>';
      }
    }

    function buildTree(filePaths) {
      const root = { name: '', children: {}, files: [] };
      for (const fp of filePaths) {
        const parts = fp.split('/');
        let node = root;
        for (let i = 0; i < parts.length - 1; i++) {
          const dir = parts[i];
          if (!node.children[dir]) {
            node.children[dir] = { name: dir, children: {}, files: [] };
          }
          node = node.children[dir];
        }
        node.files.push({ name: parts[parts.length - 1], path: fp });
      }
      return root;
    }

    function renderTreeNode(node, depth, container) {
      const sortedDirs = Object.keys(node.children).sort((a, b) => a.localeCompare(b, undefined, { sensitivity: 'base' }));
      const sortedFiles = node.files.slice().sort((a, b) => a.name.localeCompare(b.name, undefined, { sensitivity: 'base' }));

      for (const dirName of sortedDirs) {
        const child = node.children[dirName];
        const folderBtn = document.createElement('button');
        folderBtn.className = 'tree-item';
        folderBtn.type = 'button';
        folderBtn.style.paddingLeft = (depth * 16) + 'px';

        const chevron = document.createElement('span');
        chevron.className = 'tree-chevron expanded';
        chevron.innerHTML = '&#9654;';

        const icon = document.createElement('span');
        icon.className = 'tree-icon folder-icon';
        icon.innerHTML = '&#128193;';

        const label = document.createElement('span');
        label.className = 'tree-label';
        label.textContent = dirName;

        folderBtn.appendChild(chevron);
        folderBtn.appendChild(icon);
        folderBtn.appendChild(label);
        container.appendChild(folderBtn);

        const childContainer = document.createElement('div');
        childContainer.className = 'tree-children';
        container.appendChild(childContainer);

        folderBtn.addEventListener('click', () => {
          const isCollapsed = childContainer.classList.toggle('collapsed');
          chevron.classList.toggle('expanded', !isCollapsed);
          icon.innerHTML = isCollapsed ? '&#128194;' : '&#128193;';
        });

        renderTreeNode(child, depth + 1, childContainer);
      }

      for (const file of sortedFiles) {
        const btn = document.createElement('button');
        btn.className = 'tree-item';
        btn.type = 'button';
        btn.dataset.path = file.path;
        btn.style.paddingLeft = (depth * 16) + 'px';

        const chevronPlaceholder = document.createElement('span');
        chevronPlaceholder.className = 'tree-chevron placeholder';

        const icon = document.createElement('span');
        icon.className = 'tree-icon file-icon';
        icon.innerHTML = '&#128462;';

        const label = document.createElement('span');
        label.className = 'tree-label';
        label.textContent = file.name;
        label.title = file.path;

        btn.appendChild(chevronPlaceholder);
        btn.appendChild(icon);
        btn.appendChild(label);
        btn.addEventListener('click', () => openFile(file.path, true));
        container.appendChild(btn);
      }
    }

    function renderFileList() {
      fileListEl.innerHTML = '';
      const tree = buildTree(files);
      renderTreeNode(tree, 0, fileListEl);
      highlightActiveFile();
    }

    function highlightActiveFile() {
      for (const item of fileListEl.querySelectorAll('.tree-item[data-path]')) {
        item.classList.toggle('active', item.dataset.path === activeFile);
      }
    }

    async function openFile(filePath, pushState) {
      try {
        const response = await fetch('/api/file?path=' + encodeURIComponent(filePath));
        if (!response.ok) throw new Error('failed to load file');
        const payload = await response.json();

        activeFile = payload.path;
        rawContent = payload.content;
        fileNameEl.textContent = activeFile;
        rawCodeEl.textContent = rawContent;
        renderedEl.innerHTML = renderMarkdown(rawContent);
        await renderMermaid();
        highlightActiveFile();
        toggleRawBtn.classList.remove('hidden');

        if (pushState) {
          const url = new URL(window.location.href);
          url.searchParams.set('file', activeFile);
          if (sidebarHidden) {
            url.searchParams.set('fullscreen', '1');
            url.searchParams.delete('sidebar');
          } else {
            url.searchParams.delete('fullscreen');
            url.searchParams.delete('sidebar');
          }
          window.history.pushState({ file: activeFile, fullscreen: sidebarHidden }, '', url);
        }
      } catch (err) {
        renderedEl.innerHTML = '<div class="muted">Failed to load markdown file.</div>';
      }
    }

    function renderMarkdown(markdown) {
      let html = marked.parse(markdown, { gfm: true });
      html = html.replace(/<pre><code class="language-mermaid">([\s\S]*?)<\/code><\/pre>/g, (_, code) => {
        return '<div class="mermaid-block"><div class="mermaid">' + decodeHTML(code) + '</div></div>';
      });
      return html;
    }

    async function renderMermaid() {
      if (!window.mermaid) return;
      const nodes = document.querySelectorAll('.mermaid');
      if (!nodes.length) return;
      try {
        await window.mermaid.run({ nodes });
      } catch (err) {
        console.error('mermaid render failed', err);
      }
    }

    function loadThemePreference() {
      const storedTheme = window.localStorage.getItem(STORAGE_THEME_KEY);
      applyTheme(storedTheme === 'dark' ? 'dark' : 'light');
    }

    function applyTheme(theme) {
      const resolvedTheme = theme === 'light' ? 'light' : 'dark';
      document.documentElement.setAttribute('data-theme', resolvedTheme);
      themeToggleBtn.textContent = resolvedTheme === 'dark' ? 'Light Mode' : 'Dark Mode';
    }

    function isFullscreenMode(params) {
      const fullscreen = (params.get('fullscreen') || '').toLowerCase();
      if (fullscreen === '1' || fullscreen === 'true' || fullscreen === 'yes') {
        return true;
      }

      const sidebar = (params.get('sidebar') || '').toLowerCase();
      return sidebar === 'hidden' || sidebar === '0' || sidebar === 'false';
    }

    function applySidebarVisibility(hidden, pushState) {
      sidebarHidden = Boolean(hidden);
      appEl.classList.toggle('sidebar-hidden', sidebarHidden);
      toggleSidebarBtn.textContent = sidebarHidden ? 'Show Sidebar' : 'Hide Sidebar';

      if (!pushState) {
        return;
      }

      const url = new URL(window.location.href);
      if (activeFile) {
        url.searchParams.set('file', activeFile);
      }
      if (sidebarHidden) {
        url.searchParams.set('fullscreen', '1');
        url.searchParams.delete('sidebar');
      } else {
        url.searchParams.delete('fullscreen');
        url.searchParams.delete('sidebar');
      }
      window.history.pushState({ file: activeFile, fullscreen: sidebarHidden }, '', url);
    }

    function decodeHTML(text) {
      const textarea = document.createElement('textarea');
      textarea.innerHTML = text;
      return textarea.value;
    }
  </script>
</body>
</html>
`
