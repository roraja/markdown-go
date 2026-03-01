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

const mdviewerFile = ".mdviewer"

var validTags = map[string]bool{
	"DONE":        true,
	"IN-PROGRESS": true,
	"NEXT":        true,
	"IMPORTANT":   true,
	"REVISIT":     true,
	"ARCHIVE":     true,
}

type app struct {
	root string
	tpl  *template.Template
}

type pageData struct {
	Root        string
	InitialFile string
}

type mdviewerData struct {
	Tags   map[string][]string `json:"tags"`
	Opened map[string]bool     `json:"opened"`
}

// mdviewerDataLegacy handles the old single-tag format for migration.
type mdviewerDataLegacy struct {
	Tags map[string]string `json:"tags"`
}

func readMdviewerFile(dirPath string) (mdviewerData, error) {
	data := mdviewerData{Tags: make(map[string][]string), Opened: make(map[string]bool)}
	fp := filepath.Join(dirPath, mdviewerFile)
	content, err := os.ReadFile(fp)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return data, nil
		}
		return data, err
	}
	if err := json.Unmarshal(content, &data); err != nil {
		// Try legacy single-tag format
		var legacy mdviewerDataLegacy
		if err2 := json.Unmarshal(content, &legacy); err2 == nil && legacy.Tags != nil {
			data.Tags = make(map[string][]string)
			for k, v := range legacy.Tags {
				if v != "" {
					data.Tags[k] = []string{v}
				}
			}
			return data, nil
		}
		return mdviewerData{Tags: make(map[string][]string), Opened: make(map[string]bool)}, nil
	}
	if data.Tags == nil {
		data.Tags = make(map[string][]string)
	}
	if data.Opened == nil {
		data.Opened = make(map[string]bool)
	}
	return data, nil
}

func writeMdviewerFile(dirPath string, data mdviewerData) error {
	fp := filepath.Join(dirPath, mdviewerFile)
	if len(data.Tags) == 0 && len(data.Opened) == 0 {
		_ = os.Remove(fp)
		return nil
	}
	content, err := json.MarshalIndent(data, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(fp, content, 0644)
}

type allTagsResult struct {
	Tags   map[string][]string `json:"tags"`
	Opened map[string]bool     `json:"opened"`
}

// collectAllTags walks the root and reads all .mdviewer files, returning tags and opened state per file.
func collectAllTags(root string) (allTagsResult, error) {
	result := allTagsResult{
		Tags:   make(map[string][]string),
		Opened: make(map[string]bool),
	}
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() || d.Name() != mdviewerFile {
			return nil
		}
		dirPath := filepath.Dir(path)
		data, err := readMdviewerFile(dirPath)
		if err != nil {
			return nil
		}
		relDir, err := filepath.Rel(root, dirPath)
		if err != nil {
			return nil
		}
		for fileName, tags := range data.Tags {
			var relFile string
			if relDir == "." {
				relFile = fileName
			} else {
				relFile = filepath.ToSlash(filepath.Join(relDir, fileName))
			}
			result.Tags[relFile] = tags
		}
		for fileName, opened := range data.Opened {
			if opened {
				var relFile string
				if relDir == "." {
					relFile = fileName
				} else {
					relFile = filepath.ToSlash(filepath.Join(relDir, fileName))
				}
				result.Opened[relFile] = true
			}
		}
		return nil
	})
	return result, err
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
	mux.HandleFunc("/api/search", a.handleSearch)
	mux.HandleFunc("/api/tags", a.handleTags)
	mux.HandleFunc("/api/tag", a.handleSetTag)
	mux.HandleFunc("/api/opened", a.handleMarkOpened)
	mux.HandleFunc("/api/archive", a.handleArchive)
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

type searchResult struct {
	Path    string `json:"path"`
	Context string `json:"context"`
}

func (a *app) handleSearch(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	query := strings.TrimSpace(r.URL.Query().Get("q"))
	if query == "" {
		http.Error(w, "missing query parameter 'q'", http.StatusBadRequest)
		return
	}

	results, err := searchFiles(a.root, query)
	if err != nil {
		http.Error(w, "search failed", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_ = json.NewEncoder(w).Encode(struct {
		Query   string         `json:"query"`
		Results []searchResult `json:"results"`
	}{
		Query:   query,
		Results: results,
	})
}

func (a *app) handleTags(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	result, err := collectAllTags(a.root)
	if err != nil {
		http.Error(w, "failed to read tags", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_ = json.NewEncoder(w).Encode(result)
}

func (a *app) handleSetTag(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req struct {
		Path   string `json:"path"`
		Tag    string `json:"tag"`
		Action string `json:"action"` // "add", "remove", or "clear"
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}
	relPath, err := sanitizeRelativePath(req.Path)
	if err != nil {
		http.Error(w, "invalid path", http.StatusBadRequest)
		return
	}
	if !isMarkdownFile(relPath) {
		http.Error(w, "only markdown files can be tagged", http.StatusBadRequest)
		return
	}
	if req.Action == "" {
		req.Action = "add"
	}
	if req.Action != "add" && req.Action != "remove" && req.Action != "clear" {
		http.Error(w, "invalid action", http.StatusBadRequest)
		return
	}
	if req.Action != "clear" && req.Tag != "" && !validTags[req.Tag] {
		http.Error(w, "invalid tag", http.StatusBadRequest)
		return
	}

	dirRel := filepath.Dir(relPath)
	fileName := filepath.Base(relPath)
	dirAbs, err := secureJoin(a.root, dirRel)
	if err != nil {
		http.Error(w, "invalid path", http.StatusBadRequest)
		return
	}

	data, err := readMdviewerFile(dirAbs)
	if err != nil {
		http.Error(w, "failed to read tags", http.StatusInternalServerError)
		return
	}

	switch req.Action {
	case "clear":
		delete(data.Tags, fileName)
	case "remove":
		if tags, ok := data.Tags[fileName]; ok {
			filtered := make([]string, 0, len(tags))
			for _, t := range tags {
				if t != req.Tag {
					filtered = append(filtered, t)
				}
			}
			if len(filtered) == 0 {
				delete(data.Tags, fileName)
			} else {
				data.Tags[fileName] = filtered
			}
		}
	case "add":
		if req.Tag != "" {
			existing := data.Tags[fileName]
			found := false
			for _, t := range existing {
				if t == req.Tag {
					found = true
					break
				}
			}
			if !found {
				data.Tags[fileName] = append(existing, req.Tag)
			}
		}
	}

	if err := writeMdviewerFile(dirAbs, data); err != nil {
		http.Error(w, "failed to write tags", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_ = json.NewEncoder(w).Encode(struct {
		OK bool `json:"ok"`
	}{OK: true})
}

func (a *app) handleMarkOpened(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req struct {
		Path string `json:"path"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}
	relPath, err := sanitizeRelativePath(req.Path)
	if err != nil {
		http.Error(w, "invalid path", http.StatusBadRequest)
		return
	}
	if !isMarkdownFile(relPath) {
		http.Error(w, "only markdown files supported", http.StatusBadRequest)
		return
	}

	dirRel := filepath.Dir(relPath)
	fileName := filepath.Base(relPath)
	dirAbs, err := secureJoin(a.root, dirRel)
	if err != nil {
		http.Error(w, "invalid path", http.StatusBadRequest)
		return
	}

	data, err := readMdviewerFile(dirAbs)
	if err != nil {
		http.Error(w, "failed to read data", http.StatusInternalServerError)
		return
	}

	data.Opened[fileName] = true

	if err := writeMdviewerFile(dirAbs, data); err != nil {
		http.Error(w, "failed to write data", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_ = json.NewEncoder(w).Encode(struct {
		OK bool `json:"ok"`
	}{OK: true})
}

func (a *app) handleArchive(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req struct {
		Files []string `json:"files"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}

	moved := 0
	for _, f := range req.Files {
		relPath, err := sanitizeRelativePath(f)
		if err != nil || !isMarkdownFile(relPath) {
			continue
		}
		srcAbs, err := secureJoin(a.root, relPath)
		if err != nil {
			continue
		}

		dirRel := filepath.Dir(relPath)
		fileName := filepath.Base(relPath)

		// Build .archive destination
		archiveDir := filepath.Join(a.root, dirRel, ".archive")
		if err := os.MkdirAll(archiveDir, 0755); err != nil {
			continue
		}
		dstAbs := filepath.Join(archiveDir, fileName)

		if err := os.Rename(srcAbs, dstAbs); err != nil {
			continue
		}

		// Remove tag and opened state from .mdviewer
		srcDirAbs := filepath.Join(a.root, filepath.FromSlash(dirRel))
		data, err := readMdviewerFile(srcDirAbs)
		if err == nil {
			delete(data.Tags, fileName)
			delete(data.Opened, fileName)
			_ = writeMdviewerFile(srcDirAbs, data)
		}
		moved++
	}

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_ = json.NewEncoder(w).Encode(struct {
		Moved int `json:"moved"`
	}{Moved: moved})
}

func searchFiles(root, query string) ([]searchResult, error) {
	lowerQuery := strings.ToLower(query)
	var results []searchResult

	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !isMarkdownFile(d.Name()) {
			return nil
		}

		content, err := os.ReadFile(path)
		if err != nil {
			return nil // skip unreadable files
		}

		text := string(content)
		lowerText := strings.ToLower(text)
		idx := strings.Index(lowerText, lowerQuery)
		if idx < 0 {
			return nil
		}

		rel, err := filepath.Rel(root, path)
		if err != nil {
			return nil
		}

		// Extract a context snippet around the match
		contextStart := idx - 60
		if contextStart < 0 {
			contextStart = 0
		}
		contextEnd := idx + len(query) + 60
		if contextEnd > len(text) {
			contextEnd = len(text)
		}

		snippet := text[contextStart:contextEnd]
		// Clean up newlines in snippet
		snippet = strings.ReplaceAll(snippet, "\n", " ")
		snippet = strings.ReplaceAll(snippet, "\r", "")

		prefix := ""
		suffix := ""
		if contextStart > 0 {
			prefix = "…"
		}
		if contextEnd < len(text) {
			suffix = "…"
		}

		results = append(results, searchResult{
			Path:    filepath.ToSlash(rel),
			Context: prefix + snippet + suffix,
		})
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Slice(results, func(i, j int) bool {
		return results[i].Path < results[j].Path
	})
	return results, nil
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

    .search-box {
      margin-top: 12px;
      position: relative;
    }

    .search-box input {
      width: 100%;
      padding: 6px 30px 6px 10px;
      border: 1px solid var(--border);
      border-radius: 6px;
      background: var(--panel);
      color: var(--text);
      font-size: 13px;
      font-family: inherit;
      outline: none;
    }

    .search-box input:focus {
      border-color: var(--link);
    }

    .search-box input::placeholder {
      color: var(--muted);
    }

    .search-clear {
      position: absolute;
      right: 6px;
      top: 50%;
      transform: translateY(-50%);
      border: none;
      background: transparent;
      color: var(--muted);
      cursor: pointer;
      font-size: 14px;
      padding: 0 4px;
      line-height: 1;
      display: none;
    }

    .search-clear:hover { color: var(--text); }

    .search-result-item {
      display: block;
      width: 100%;
      border: none;
      background: transparent;
      color: var(--text);
      text-align: left;
      padding: 8px 8px;
      cursor: pointer;
      font-size: 13px;
      font-family: inherit;
      border-bottom: 1px solid var(--border);
    }

    .search-result-item:last-child { border-bottom: none; }
    .search-result-item:hover { background: var(--panel); }
    .search-result-item.active { background: var(--active); }

    .search-result-path {
      font-weight: 600;
      margin-bottom: 3px;
      white-space: nowrap;
      overflow: hidden;
      text-overflow: ellipsis;
    }

    .search-result-context {
      font-size: 12px;
      color: var(--muted);
      line-height: 1.4;
      word-break: break-word;
    }

    .search-result-context mark {
      background: #f0b429;
      color: #1a1a1a;
      border-radius: 2px;
      padding: 0 1px;
    }

    .search-info {
      font-size: 12px;
      color: var(--muted);
      padding: 8px 0;
    }

    mark.search-highlight {
      background: #f0b429;
      color: #1a1a1a;
      border-radius: 2px;
      padding: 0 1px;
      scroll-margin-top: 80px;
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
    .btn:disabled {
      opacity: 0.4;
      cursor: not-allowed;
    }
    .btn:disabled:hover { background: var(--button-bg); }

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

    .tree-tag {
      margin-left: auto;
      font-size: 12px;
      line-height: 1;
      flex-shrink: 0;
      padding-left: 4px;
    }

    .tag-menu {
      position: fixed;
      z-index: 1000;
      background: var(--panel);
      border: 1px solid var(--border);
      border-radius: 8px;
      padding: 4px 0;
      min-width: 160px;
      box-shadow: 0 8px 24px rgba(0,0,0,0.3);
    }

    .tag-menu-item {
      display: flex;
      align-items: center;
      gap: 8px;
      width: 100%;
      border: none;
      background: transparent;
      color: var(--text);
      text-align: left;
      padding: 6px 12px;
      cursor: pointer;
      font-size: 13px;
      font-family: inherit;
    }

    .tag-menu-item:hover { background: var(--active); }

    .tag-menu-divider {
      height: 1px;
      background: var(--border);
      margin: 4px 0;
    }

    .header-tags {
      display: flex;
      gap: 4px;
      flex-wrap: wrap;
      margin-top: 6px;
    }

    .header-tag-btn {
      border: 1px solid var(--border);
      border-radius: 12px;
      background: var(--button-bg);
      color: var(--muted);
      padding: 2px 10px;
      cursor: pointer;
      font-size: 12px;
      font-family: inherit;
      line-height: 1.5;
      transition: background 0.15s, border-color 0.15s;
    }

    .header-tag-btn:hover { background: var(--button-hover); }

    .header-tag-btn.active {
      background: var(--active);
      border-color: var(--link);
      color: var(--text);
    }

    .tag-filter-wrapper {
      position: relative;
      margin-top: 6px;
    }

    .tag-filter-btn {
      display: flex;
      align-items: center;
      gap: 4px;
      width: 100%;
      padding: 5px 10px;
      border: 1px solid var(--border);
      border-radius: 6px;
      background: var(--panel);
      color: var(--muted);
      font-size: 12px;
      font-family: inherit;
      cursor: pointer;
      text-align: left;
    }

    .tag-filter-btn:hover { border-color: var(--link); }

    .tag-filter-dropdown {
      position: absolute;
      top: 100%;
      left: 0;
      right: 0;
      z-index: 100;
      background: var(--panel);
      border: 1px solid var(--border);
      border-radius: 6px;
      margin-top: 2px;
      padding: 4px 0;
      box-shadow: 0 4px 12px rgba(0,0,0,0.2);
    }

    .tag-filter-option {
      display: flex;
      align-items: center;
      gap: 6px;
      width: 100%;
      border: none;
      background: transparent;
      color: var(--text);
      text-align: left;
      padding: 4px 10px;
      cursor: pointer;
      font-size: 12px;
      font-family: inherit;
    }

    .tag-filter-option:hover { background: var(--active); }

    .tag-filter-option input[type="checkbox"] {
      margin: 0;
      accent-color: var(--link);
    }
  </style>
</head>
<body>
  <div class="app">
    <aside class="sidebar">
      <h1>Markdown Files</h1>
      <div class="root-path">{{ .Root }}</div>
      <div class="search-box">
        <input type="text" id="search-input" placeholder="Search in files…" autocomplete="off" />
        <button class="search-clear" id="search-clear" type="button">&times;</button>
      </div>
      <div class="tag-filter-wrapper">
        <button class="tag-filter-btn" id="tag-filter-btn" type="button">🏷️ Filter by tags ▾</button>
        <div class="tag-filter-dropdown hidden" id="tag-filter-dropdown"></div>
      </div>
      <div class="files" id="file-list">
        <div class="muted">Loading files…</div>
      </div>
    </aside>
    <main class="main">
      <div class="header">
        <div>
          <h2 id="file-name">Select a markdown file</h2>
          <div class="muted">GitHub-like markdown preview with Mermaid support</div>
          <div class="header-tags hidden" id="header-tags"></div>
        </div>
        <div class="header-actions">
          <button id="prev-file-btn" class="btn nav-btn hidden" type="button" title="Previous file">&#9664; Prev</button>
          <button id="next-file-btn" class="btn nav-btn hidden" type="button" title="Next file">Next &#9654;</button>
          <button id="toggle-sidebar-btn" class="btn" type="button">Hide Sidebar</button>
          <button id="theme-toggle-btn" class="btn" type="button">Light Mode</button>
          <button id="toggle-raw-btn" class="btn hidden" type="button">Show Raw</button>
          <button id="archive-btn" class="btn" type="button" title="Move all ARCHIVE-tagged files to .archive folder">&#128230; Archive</button>
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
    const prevFileBtn = document.getElementById('prev-file-btn');
    const nextFileBtn = document.getElementById('next-file-btn');
    const searchInput = document.getElementById('search-input');
    const searchClear = document.getElementById('search-clear');
    const headerTagsEl = document.getElementById('header-tags');
    const tagFilterBtn = document.getElementById('tag-filter-btn');
    const tagFilterDropdown = document.getElementById('tag-filter-dropdown');
    const archiveBtn = document.getElementById('archive-btn');
    const STORAGE_THEME_KEY = 'mdviewer-theme';

    let files = [];
    let activeFile = '';
    let rawContent = '';
    let showingRaw = false;
    let sidebarHidden = false;
    let searchTimer = null;
    let searchMode = false;
    let baseFolderPath = '';
    let fileTags = {};
    let fileOpened = {};
    let activeTagFilters = new Set();

    const TAG_ICONS = {
      'DONE': '\u2705',
      'IN-PROGRESS': '\uD83D\uDD04',
      'NEXT': '\u23ED\uFE0F',
      'IMPORTANT': '\u2B50',
      'REVISIT': '\uD83D\uDD01',
      'ARCHIVE': '\uD83D\uDCE6',
      'UNREAD': '\uD83D\uDCD5'
    };
    const TAG_LIST = ['DONE', 'IN-PROGRESS', 'NEXT', 'IMPORTANT', 'REVISIT', 'ARCHIVE'];
    const ALL_FILTER_TAGS = ['UNREAD', ...TAG_LIST];

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

    prevFileBtn.addEventListener('click', () => navigateFile(-1));
    nextFileBtn.addEventListener('click', () => navigateFile(1));

    archiveBtn.addEventListener('click', async () => {
      const archiveFiles = files.filter(f => (fileTags[f] || []).includes('ARCHIVE'));
      if (archiveFiles.length === 0) {
        alert('No files tagged with ARCHIVE.');
        return;
      }
      if (!confirm('Move ' + archiveFiles.length + ' file(s) tagged ARCHIVE to .archive folder?')) return;
      try {
        const resp = await fetch('/api/archive', {
          method: 'POST',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify({ files: archiveFiles })
        });
        if (!resp.ok) { const t = await resp.text(); throw new Error(t); }
        const result = await resp.json();
        // Refresh file list and tags
        const [filesResp, tagsResp] = await Promise.all([fetch('/api/files'), fetch('/api/tags')]);
        if (filesResp.ok) {
          const p = await filesResp.json();
          let allFiles = p.files || [];
          if (baseFolderPath) {
            const prefix = baseFolderPath + '/';
            files = allFiles.filter(f => f.startsWith(prefix) || f === baseFolderPath);
          } else {
            files = allFiles;
          }
        }
        if (tagsResp.ok) {
          const tp = await tagsResp.json();
          fileTags = tp.tags || {};
          fileOpened = tp.opened || {};
        }
        renderFileList();
        if (activeFile && !files.includes(activeFile)) {
          if (files.length > 0) {
            await openFile(files[0], true);
          } else {
            activeFile = '';
            fileNameEl.textContent = 'Select a markdown file';
            renderedEl.innerHTML = '<div class="muted">No markdown files found.</div>';
            renderHeaderTags();
          }
        }
        alert('Archived ' + result.moved + ' file(s).');
      } catch (err) {
        alert('Archive failed: ' + err.message);
      }
    });

    searchInput.addEventListener('input', () => {
      const query = searchInput.value.trim();
      searchClear.style.display = query ? 'block' : 'none';
      clearTimeout(searchTimer);
      if (!query) {
        searchMode = false;
        renderFileList();
        return;
      }
      searchTimer = setTimeout(() => performSearch(query), 250);
    });

    searchClear.addEventListener('click', () => {
      searchInput.value = '';
      searchClear.style.display = 'none';
      searchMode = false;
      renderFileList();
    });

    tagFilterBtn.addEventListener('click', (e) => {
      e.stopPropagation();
      const isHidden = tagFilterDropdown.classList.toggle('hidden');
      if (!isHidden) {
        renderTagFilterDropdown();
        setTimeout(() => document.addEventListener('click', closeTagFilter, { once: true }), 0);
      }
    });

    function closeTagFilter() {
      tagFilterDropdown.classList.add('hidden');
    }

    function renderTagFilterDropdown() {
      tagFilterDropdown.innerHTML = '';
      for (const tag of ALL_FILTER_TAGS) {
        const label = document.createElement('label');
        label.className = 'tag-filter-option';
        const cb = document.createElement('input');
        cb.type = 'checkbox';
        cb.checked = activeTagFilters.has(tag);
        cb.addEventListener('change', (e) => {
          e.stopPropagation();
          if (cb.checked) {
            activeTagFilters.add(tag);
          } else {
            activeTagFilters.delete(tag);
          }
          updateTagFilterBtnLabel();
          if (!searchMode) renderFileList();
        });
        label.appendChild(cb);
        label.appendChild(document.createTextNode(TAG_ICONS[tag] + ' ' + tag));
        label.addEventListener('click', (e) => e.stopPropagation());
        tagFilterDropdown.appendChild(label);
      }
    }

    function updateTagFilterBtnLabel() {
      if (activeTagFilters.size === 0) {
        tagFilterBtn.textContent = '\uD83C\uDFF7\uFE0F Filter by tags \u25BE';
      } else {
        const icons = [...activeTagFilters].map(t => TAG_ICONS[t] || t).join(' ');
        tagFilterBtn.textContent = '\uD83C\uDFF7\uFE0F ' + icons + ' \u25BE';
      }
    }

    function getEffectiveTags(filePath) {
      const tags = (fileTags[filePath] || []).slice();
      if (!fileOpened[filePath]) {
        tags.push('UNREAD');
      }
      return tags;
    }

    function fileMatchesTagFilter(filePath) {
      if (activeTagFilters.size === 0) return true;
      const tags = getEffectiveTags(filePath);
      for (const f of activeTagFilters) {
        if (tags.includes(f)) return true;
      }
      return false;
    }

    function navigateFile(direction) {
      if (!files.length || !activeFile) return;
      const idx = files.indexOf(activeFile);
      const next = idx + direction;
      if (next >= 0 && next < files.length) {
        openFile(files[next], true);
      }
    }

    function updateNavButtons() {
      if (!files.length || !activeFile) {
        prevFileBtn.classList.add('hidden');
        nextFileBtn.classList.add('hidden');
        return;
      }
      const idx = files.indexOf(activeFile);
      prevFileBtn.classList.remove('hidden');
      nextFileBtn.classList.remove('hidden');
      prevFileBtn.disabled = idx <= 0;
      nextFileBtn.disabled = idx >= files.length - 1;
    }

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
        const params = new URLSearchParams(window.location.search);
        const rawBase = (params.get('baseFolderPath') || '').replace(/\/+$/, '').replace(/^\/+/, '');
        baseFolderPath = rawBase;

        const [filesResp, tagsResp] = await Promise.all([
          fetch('/api/files'),
          fetch('/api/tags')
        ]);
        if (!filesResp.ok) throw new Error('failed to list files');
        const payload = await filesResp.json();
        let allFiles = payload.files || [];

        if (tagsResp.ok) {
          const tagsPayload = await tagsResp.json();
          fileTags = tagsPayload.tags || {};
          fileOpened = tagsPayload.opened || {};
        }

        if (baseFolderPath) {
          const prefix = baseFolderPath + '/';
          files = allFiles.filter(f => f.startsWith(prefix) || f === baseFolderPath);
        } else {
          files = allFiles;
        }
        renderFileList();

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
        chevron.className = 'tree-chevron';
        chevron.innerHTML = '&#9654;';

        const icon = document.createElement('span');
        icon.className = 'tree-icon folder-icon';
        icon.innerHTML = '&#128194;';

        const label = document.createElement('span');
        label.className = 'tree-label';
        label.textContent = dirName;

        folderBtn.appendChild(chevron);
        folderBtn.appendChild(icon);
        folderBtn.appendChild(label);
        container.appendChild(folderBtn);

        const childContainer = document.createElement('div');
        childContainer.className = 'tree-children collapsed';
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

        const fullFilePath = baseFolderPath ? baseFolderPath + '/' + file.path : file.path;
        const tags = getEffectiveTags(fullFilePath);
        if (tags.length > 0) {
          const tagSpan = document.createElement('span');
          tagSpan.className = 'tree-tag';
          tagSpan.textContent = tags.map(t => TAG_ICONS[t] || '').filter(Boolean).join('');
          tagSpan.title = tags.join(', ');
          btn.appendChild(tagSpan);
        }

        btn.addEventListener('click', () => {
          openFile(fullFilePath, true);
        });
        btn.addEventListener('contextmenu', (e) => {
          e.preventDefault();
          showTagMenu(e.clientX, e.clientY, fullFilePath);
        });
        container.appendChild(btn);
      }
    }

    function renderFileList() {
      fileListEl.innerHTML = '';
      let displayFiles = files;
      if (baseFolderPath) {
        const prefix = baseFolderPath + '/';
        displayFiles = files.map(f => f.startsWith(prefix) ? f.slice(prefix.length) : f);
      }
      if (activeTagFilters.size > 0) {
        displayFiles = displayFiles.filter(f => {
          const fullPath = baseFolderPath ? baseFolderPath + '/' + f : f;
          return fileMatchesTagFilter(fullPath);
        });
      }
      const tree = buildTree(displayFiles);
      renderTreeNode(tree, 0, fileListEl);
      highlightActiveFile();
    }

    function highlightActiveFile() {
      for (const item of fileListEl.querySelectorAll('.tree-item[data-path]')) {
        let displayPath = item.dataset.path;
        if (baseFolderPath) displayPath = baseFolderPath + '/' + displayPath;
        const isActive = displayPath === activeFile;
        item.classList.toggle('active', isActive);
        if (isActive) expandAncestors(item);
      }
      for (const item of fileListEl.querySelectorAll('.search-result-item')) {
        const pathDiv = item.querySelector('.search-result-path');
        item.classList.toggle('active', pathDiv && pathDiv.textContent === activeFile);
      }
    }

    function expandAncestors(el) {
      let node = el.parentElement;
      while (node && node !== fileListEl) {
        if (node.classList.contains('tree-children') && node.classList.contains('collapsed')) {
          node.classList.remove('collapsed');
          const folderBtn = node.previousElementSibling;
          if (folderBtn) {
            const chevron = folderBtn.querySelector('.tree-chevron');
            const icon = folderBtn.querySelector('.tree-icon');
            if (chevron) chevron.classList.add('expanded');
            if (icon) icon.innerHTML = '&#128193;';
          }
        }
        node = node.parentElement;
      }
    }

    async function openFile(filePath, pushState, searchQuery) {
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
        updateNavButtons();
        renderHeaderTags();

        // Mark as opened if not already
        if (!fileOpened[activeFile]) {
          fileOpened[activeFile] = true;
          fetch('/api/opened', {
            method: 'POST',
            headers: { 'Content-Type': 'application/json' },
            body: JSON.stringify({ path: activeFile })
          }).catch(() => {});
          if (!searchMode) renderFileList();
        }

        // Clear previous highlights
        renderedEl.querySelectorAll('mark.search-highlight').forEach(m => {
          m.replaceWith(m.textContent);
        });

        if (searchQuery) {
          scrollToMatch(renderedEl, searchQuery);
        }

        if (pushState) {
          const url = new URL(window.location.href);
          url.searchParams.set('file', activeFile);
          if (baseFolderPath) url.searchParams.set('baseFolderPath', baseFolderPath);
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
      if (baseFolderPath) url.searchParams.set('baseFolderPath', baseFolderPath);
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

    async function performSearch(query) {
      try {
        const response = await fetch('/api/search?q=' + encodeURIComponent(query));
        if (!response.ok) throw new Error('search failed');
        const payload = await response.json();
        searchMode = true;
        renderSearchResults(payload.results, payload.query);
      } catch (err) {
        fileListEl.innerHTML = '<div class="muted">Search failed.</div>';
      }
    }

    function renderSearchResults(results, query) {
      fileListEl.innerHTML = '';

      const info = document.createElement('div');
      info.className = 'search-info';
      info.textContent = results.length + ' result' + (results.length !== 1 ? 's' : '') + ' found';
      fileListEl.appendChild(info);

      if (results.length === 0) return;

      for (const r of results) {
        const btn = document.createElement('button');
        btn.className = 'search-result-item';
        btn.type = 'button';
        if (r.path === activeFile) btn.classList.add('active');

        const pathDiv = document.createElement('div');
        pathDiv.className = 'search-result-path';
        pathDiv.textContent = r.path;

        const ctxDiv = document.createElement('div');
        ctxDiv.className = 'search-result-context';
        ctxDiv.innerHTML = highlightQuery(escapeHtml(r.context), query);

        btn.appendChild(pathDiv);
        btn.appendChild(ctxDiv);
        btn.addEventListener('click', () => openFile(r.path, true, query));
        fileListEl.appendChild(btn);
      }
    }

    function highlightQuery(text, query) {
      const escaped = query.replace(/[.*+?^${}()|[\]\\]/g, '\\$&');
      const re = new RegExp('(' + escaped + ')', 'gi');
      return text.replace(re, '<mark>$1</mark>');
    }

    function escapeHtml(str) {
      const div = document.createElement('div');
      div.textContent = str;
      return div.innerHTML;
    }

    function scrollToMatch(container, query) {
      const walker = document.createTreeWalker(container, NodeFilter.SHOW_TEXT, null);
      const lowerQuery = query.toLowerCase();
      let node;
      while ((node = walker.nextNode())) {
        const idx = node.textContent.toLowerCase().indexOf(lowerQuery);
        if (idx < 0) continue;

        const range = document.createRange();
        range.setStart(node, idx);
        range.setEnd(node, idx + query.length);

        const mark = document.createElement('mark');
        mark.className = 'search-highlight';
        range.surroundContents(mark);

        mark.scrollIntoView({ behavior: 'smooth', block: 'center' });
        return;
      }
    }

    function showTagMenu(x, y, filePath) {
      closeTagMenu();
      const menu = document.createElement('div');
      menu.className = 'tag-menu';
      menu.id = 'tag-context-menu';

      const currentTags = fileTags[filePath] || [];

      for (const tag of TAG_LIST) {
        const item = document.createElement('button');
        item.className = 'tag-menu-item';
        item.type = 'button';
        const hasTag = currentTags.includes(tag);
        const prefix = hasTag ? '☑ ' : '☐ ';
        item.textContent = prefix + TAG_ICONS[tag] + ' ' + tag;
        item.addEventListener('click', (e) => {
          e.stopPropagation();
          if (hasTag) {
            setTag(filePath, tag, 'remove');
          } else {
            setTag(filePath, tag, 'add');
          }
          closeTagMenu();
        });
        menu.appendChild(item);
      }

      if (currentTags.length > 0) {
        const divider = document.createElement('div');
        divider.className = 'tag-menu-divider';
        menu.appendChild(divider);

        const clearItem = document.createElement('button');
        clearItem.className = 'tag-menu-item';
        clearItem.type = 'button';
        clearItem.textContent = '  ✕ Remove all tags';
        clearItem.addEventListener('click', () => { closeTagMenu(); setTag(filePath, '', 'clear'); });
        menu.appendChild(clearItem);
      }

      menu.style.left = x + 'px';
      menu.style.top = y + 'px';
      document.body.appendChild(menu);

      const rect = menu.getBoundingClientRect();
      if (rect.right > window.innerWidth) menu.style.left = (window.innerWidth - rect.width - 8) + 'px';
      if (rect.bottom > window.innerHeight) menu.style.top = (window.innerHeight - rect.height - 8) + 'px';

      setTimeout(() => document.addEventListener('click', closeTagMenu, { once: true }), 0);
    }

    function closeTagMenu() {
      const existing = document.getElementById('tag-context-menu');
      if (existing) existing.remove();
    }

    async function setTag(filePath, tag, action) {
      try {
        const resp = await fetch('/api/tag', {
          method: 'POST',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify({ path: filePath, tag: tag, action: action || 'add' })
        });
        if (!resp.ok) throw new Error('failed to set tag');

        // Update local state
        if (action === 'clear') {
          delete fileTags[filePath];
        } else if (action === 'remove') {
          const arr = fileTags[filePath] || [];
          fileTags[filePath] = arr.filter(t => t !== tag);
          if (fileTags[filePath].length === 0) delete fileTags[filePath];
        } else {
          const arr = fileTags[filePath] || [];
          if (!arr.includes(tag)) {
            fileTags[filePath] = [...arr, tag];
          }
        }
        if (!searchMode) renderFileList();
        if (filePath === activeFile) renderHeaderTags();
      } catch (err) {
        console.error('Failed to set tag:', err);
      }
    }

    function renderHeaderTags() {
      headerTagsEl.innerHTML = '';
      if (!activeFile) {
        headerTagsEl.classList.add('hidden');
        return;
      }
      headerTagsEl.classList.remove('hidden');
      const currentTags = fileTags[activeFile] || [];
      for (const tag of TAG_LIST) {
        const btn = document.createElement('button');
        btn.className = 'header-tag-btn';
        btn.type = 'button';
        if (currentTags.includes(tag)) btn.classList.add('active');
        btn.textContent = TAG_ICONS[tag] + ' ' + tag;
        btn.addEventListener('click', () => {
          if (currentTags.includes(tag)) {
            setTag(activeFile, tag, 'remove');
          } else {
            setTag(activeFile, tag, 'add');
          }
        });
        headerTagsEl.appendChild(btn);
      }
    }
  </script>
</body>
</html>
`
