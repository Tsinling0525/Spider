package dashboard

import (
	"archive/zip"
	"compress/gzip"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

var memoryMethods = map[string]bool{
	"handshake": true, "stats": true, "memory_search": true, "memory_get": true,
	"list_atoms": true, "list_raw_events": true, "list_entities": true, "list_episodes": true, "list_journal": true, "list_candidates": true,
	"get_atom": true, "get_raw_event": true, "get_entity": true, "get_episode": true, "get_candidate": true,
	"stats_counts": true, "stats_growth": true, "stats_atom_kinds": true, "recent_journal": true,
	"terminal_about_me": true, "terminal_current_focus": true, "terminal_things_you_told_me": true, "terminal_recent_stories": true, "terminal_entities": true,
	"create_atom": true, "replace_atom": true, "deprecate_atom": true, "promote_candidate": true, "reject_candidate": true,
	"tree": true, "storage_check": true,
}

func registerMemoryRoutes(mux *http.ServeMux, s *Service) {
	call := func(w http.ResponseWriter, r *http.Request, method string, params map[string]any) {
		job := s.memoryStatus()
		if job.Status == "running" && job.Kind == "slim" {
			respondError(w, errBusy)
			return
		}
		if !memoryMethods[method] {
			respondError(w, errInvalid)
			return
		}
		result, err := s.memoryRPC(r.Context(), method, params, nil, nil)
		if err != nil {
			respondError(w, err)
			return
		}
		respond(w, 200, result)
	}
	mux.HandleFunc("POST /v1/dashboard/memory/rpc", func(w http.ResponseWriter, r *http.Request) {
		var input struct {
			Method string         `json:"method"`
			Params map[string]any `json:"params"`
		}
		if !decode(w, r, &input) {
			return
		}
		call(w, r, input.Method, input.Params)
	})
	for _, resource := range []string{"atoms", "raw_events", "entities", "episodes", "journal", "candidates"} {
		mux.HandleFunc("POST /v1/dashboard/memory/"+resource+"/list", func(w http.ResponseWriter, r *http.Request) {
			params := map[string]any{}
			if !decode(w, r, &params) {
				return
			}
			call(w, r, "list_"+resource, params)
		})
		if resource == "journal" {
			continue
		}
		singular := strings.TrimSuffix(resource, "s")
		if resource == "entities" {
			singular = "entity"
		}
		mux.HandleFunc("GET /v1/dashboard/memory/"+resource+"/{id}", func(w http.ResponseWriter, r *http.Request) {
			call(w, r, "get_"+singular, map[string]any{"id": r.PathValue("id")})
		})
	}
	for path, method := range map[string]string{"stats/counts": "stats_counts", "stats/growth": "stats_growth", "stats/atom_kinds": "stats_atom_kinds", "journal/recent": "recent_journal", "terminal/about_me": "terminal_about_me", "terminal/current_focus": "terminal_current_focus", "terminal/things_you_told_me": "terminal_things_you_told_me", "terminal/recent_stories": "terminal_recent_stories", "terminal/entities": "terminal_entities", "tree": "tree", "storage": "storage_check"} {
		mux.HandleFunc("GET /v1/dashboard/memory/"+path, func(w http.ResponseWriter, r *http.Request) {
			params := map[string]any{}
			for _, key := range []string{"days", "limit"} {
				if value := r.URL.Query().Get(key); value != "" {
					number, err := strconv.Atoi(value)
					if err != nil {
						respondError(w, errInvalid)
						return
					}
					params[key] = number
				}
			}
			call(w, r, method, params)
		})
	}
	mux.HandleFunc("POST /v1/dashboard/memory/atoms", func(w http.ResponseWriter, r *http.Request) {
		params := map[string]any{}
		if !decode(w, r, &params) {
			return
		}
		call(w, r, "create_atom", params)
	})
	for path, method := range map[string]string{"atoms/{id}/replace": "replace_atom", "atoms/{id}/deprecate": "deprecate_atom", "candidates/{id}/promote": "promote_candidate", "candidates/{id}/reject": "reject_candidate"} {
		mux.HandleFunc("POST /v1/dashboard/memory/"+path, func(w http.ResponseWriter, r *http.Request) {
			params := map[string]any{}
			if !decode(w, r, &params) {
				return
			}
			params["id"] = r.PathValue("id")
			call(w, r, method, params)
		})
	}
	for _, path := range []string{"config", "extract-config"} {
		mux.HandleFunc("GET /v1/dashboard/memory/"+path, func(w http.ResponseWriter, r *http.Request) { respond(w, 200, s.memoryConfig()) })
		mux.HandleFunc("PUT /v1/dashboard/memory/"+path, func(w http.ResponseWriter, r *http.Request) {
			config := s.memoryConfig()
			if !decode(w, r, &config) {
				return
			}
			if err := s.saveMemoryConfig(config); err != nil {
				respondError(w, err)
				return
			}
			respond(w, 200, config)
		})
	}
	mux.HandleFunc("GET /v1/dashboard/memory/maintenance", func(w http.ResponseWriter, r *http.Request) { respond(w, 200, s.memoryStatus()) })
	mux.HandleFunc("POST /v1/dashboard/memory/jobs", func(w http.ResponseWriter, r *http.Request) {
		var input struct {
			Kind    string `json:"kind"`
			Session string `json:"session_id"`
		}
		if !decode(w, r, &input) {
			return
		}
		job, err := s.startMemoryJob(input.Kind, input.Session)
		if err != nil {
			respondError(w, err)
			return
		}
		respond(w, 202, job)
	})
	mux.HandleFunc("POST /v1/dashboard/memory/portable/pack", func(w http.ResponseWriter, r *http.Request) {
		if !memoryPortableReady(w, s) {
			return
		}
		file, err := os.CreateTemp(s.memory.root, "export-*.hmpkg")
		if err != nil {
			respondError(w, err)
			return
		}
		path := file.Name()
		file.Close()
		defer os.Remove(path)
		if _, err := s.memoryRPC(r.Context(), "pack", map[string]any{"package_path": path}, nil, nil); err != nil {
			respondError(w, err)
			return
		}
		w.Header().Set("Content-Type", "application/octet-stream")
		w.Header().Set("Content-Disposition", `attachment; filename="spider-memory.hmpkg"`)
		http.ServeFile(w, r, path)
	})
	mux.HandleFunc("POST /v1/dashboard/memory/portable/adopt", func(w http.ResponseWriter, r *http.Request) {
		if !memoryPortableReady(w, s) {
			return
		}
		path, err := memoryUpload(w, r, s)
		if err != nil {
			respondError(w, err)
			return
		}
		defer os.Remove(path)
		conflict := r.FormValue("on_conflict")
		if conflict == "" {
			conflict = "skip"
		}
		if conflict != "skip" && conflict != "replace" && conflict != "raise" {
			respondError(w, errInvalid)
			return
		}
		dryRun := r.FormValue("dry_run") != "false"
		result, err := s.memoryRPC(r.Context(), "adopt", map[string]any{"package_path": path, "on_conflict": conflict, "dry_run": dryRun}, nil, nil)
		if err != nil {
			respondError(w, err)
			return
		}
		respond(w, 200, result)
	})
	mux.HandleFunc("GET /v1/dashboard/memory/portable/doctor", func(w http.ResponseWriter, r *http.Request) {
		if !memoryPortableReady(w, s) {
			return
		}
		result, err := s.memoryRPC(r.Context(), "doctor", nil, nil, nil)
		if err != nil {
			respondError(w, err)
			return
		}
		respond(w, 200, result)
	})
}

func memoryPortableReady(w http.ResponseWriter, s *Service) bool {
	if s.memory == nil {
		respondError(w, fmt.Errorf("记忆引擎未启动"))
		return false
	}
	if s.memoryStatus().Status == "running" {
		respondError(w, errBusy)
		return false
	}
	return true
}

func memoryUpload(w http.ResponseWriter, r *http.Request, s *Service) (string, error) {
	r.Body = http.MaxBytesReader(w, r.Body, 64<<20)
	if err := r.ParseMultipartForm(2 << 20); err != nil {
		return "", fmt.Errorf("%w: 记忆包上传失败或超过 64 MiB", errInvalid)
	}
	defer r.MultipartForm.RemoveAll()
	input, _, err := r.FormFile("pkg_file")
	if err != nil {
		return "", errInvalid
	}
	defer input.Close()
	file, err := os.CreateTemp(s.memory.root, "import-*.hmpkg")
	if err != nil {
		return "", err
	}
	defer file.Close()
	path := filepath.Clean(file.Name())
	_, err = io.Copy(file, input)
	if err == nil {
		err = validateMemoryPackage(path)
	}
	if err != nil {
		os.Remove(path)
		return "", err
	}
	return path, nil
}

func validateMemoryPackage(path string) error {
	archive, err := zip.OpenReader(path)
	if err != nil {
		return fmt.Errorf("%w: 无效的 .hmpkg 文件", errInvalid)
	}
	defer archive.Close()
	var total int64
	if len(archive.File) > 8 {
		return fmt.Errorf("%w: 记忆包文件数过多", errInvalid)
	}
	for _, entry := range archive.File {
		if entry.UncompressedSize64 > 256<<20 {
			return fmt.Errorf("%w: 记忆包展开后过大", errInvalid)
		}
		reader, err := entry.Open()
		if err != nil {
			return errInvalid
		}
		var content io.Reader = reader
		var compressed *gzip.Reader
		if strings.HasSuffix(entry.Name, ".gz") {
			compressed, err = gzip.NewReader(reader)
			if err != nil {
				reader.Close()
				return errInvalid
			}
			content = compressed
		}
		n, readErr := io.Copy(io.Discard, io.LimitReader(content, (256<<20)+1))
		total += n
		if compressed != nil {
			compressed.Close()
		}
		reader.Close()
		if readErr != nil || total > 256<<20 {
			return fmt.Errorf("%w: 记忆包展开后超过 256 MiB 或已损坏", errInvalid)
		}
	}
	return nil
}
