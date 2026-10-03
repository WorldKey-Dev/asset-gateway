package main

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
)

const (
	kindGLB       = "glb"
	kindThumbnail = "thumbnail"
	kindRenders   = "renders"
)

// Server：资产分发只读网关（模块热路径，§7.7）。
//
//	GET /healthz
//	GET /assets/{asset_id}/glb|thumbnail|renders/{i}
//
// ETag / Range / Cache-Control 全支持。
//
// 本服务不做任何鉴权，且网关侧刻意把 /assets 配成公开路由（挂 auth-verify 会让
// Cache-Control: public, immutable 失去 CDN 共享缓存的意义）。因此 assets 表里的
// 每一行都是全网可读的：准入控制的责任在写入方（console），只有确认可公开的资产
// 才允许进表。asset_id 也因而可枚举，不要用它承载任何保密语义。
type Server struct {
	store        AssetStore
	objects      ObjectSource
	cacheControl string
	mux          *http.ServeMux
}

func NewServer(store AssetStore, objects ObjectSource, cacheControl string) *Server {
	s := &Server{store: store, objects: objects, cacheControl: cacheControl}
	s.mux = http.NewServeMux()
	s.mux.HandleFunc("GET /healthz", s.handleHealth)
	s.mux.HandleFunc("GET /assets/", s.handleAsset)
	return s
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.mux.ServeHTTP(w, r)
}

func (s *Server) handleHealth(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) handleAsset(w http.ResponseWriter, r *http.Request) {
	assetID, kind, index, ok := parseAssetPath(r.URL.Path)
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
		return
	}
	asset, err := s.store.GetAsset(r.Context(), assetID)
	if errors.Is(err, ErrNotFound) {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "asset not found"})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "asset lookup failed"})
		return
	}

	key, contentType, knownETag := resolveKind(asset, kind, index)
	if key == "" {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "asset part not ready"})
		return
	}
	if knownETag != "" && ifNoneMatch(r) == knownETag {
		w.Header().Set("ETag", knownETag)
		w.WriteHeader(http.StatusNotModified)
		return
	}

	data, err := s.objects.Get(r.Context(), key, r.Header.Get("Range"))
	if errors.Is(err, ErrObjectNotFound) {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "object not found"})
		return
	}
	if errors.Is(err, ErrInvalidRange) {
		writeJSON(w, http.StatusRequestedRangeNotSatisfiable, map[string]string{"error": "invalid range"})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": "object fetch failed"})
		return
	}
	defer data.Body.Close()

	etag := knownETag
	if etag == "" {
		etag = data.ETag
	}
	if etag != "" && ifNoneMatch(r) == etag {
		w.Header().Set("ETag", etag)
		w.WriteHeader(http.StatusNotModified)
		return
	}

	h := w.Header()
	h.Set("Content-Type", contentType)
	h.Set("Cache-Control", s.cacheControl)
	h.Set("Accept-Ranges", "bytes")
	if etag != "" {
		h.Set("ETag", etag)
	}
	if data.ContentRange != "" {
		h.Set("Content-Range", data.ContentRange)
	}
	w.WriteHeader(data.StatusCode)
	_, _ = io.Copy(w, data.Body)
}

// parseAssetPath：/assets/{id}/glb | /assets/{id}/thumbnail | /assets/{id}/renders/{i}
func parseAssetPath(path string) (assetID, kind string, index int, ok bool) {
	parts := strings.Split(strings.Trim(path, "/"), "/")
	if len(parts) < 3 || parts[0] != "assets" {
		return "", "", 0, false
	}
	assetID, kind = parts[1], parts[2]
	switch kind {
	case kindGLB, kindThumbnail:
		if len(parts) != 3 {
			return "", "", 0, false
		}
		return assetID, kind, 0, true
	case kindRenders:
		if len(parts) != 4 {
			return "", "", 0, false
		}
		i, err := strconv.Atoi(parts[3])
		if err != nil || i < 0 {
			return "", "", 0, false
		}
		return assetID, kind, i, true
	}
	return "", "", 0, false
}

// resolveKind：按产物类型取对象键与内容类型；键为空表示该产物尚未就绪。
func resolveKind(asset *Asset, kind string, index int) (key, contentType, etag string) {
	switch kind {
	case kindGLB:
		return asset.GLBPath, "model/gltf-binary", quoteETag(asset.GLBHash)
	case kindThumbnail:
		return asset.Thumbnail, "image/png", ""
	case kindRenders:
		if index >= len(asset.Renders) {
			return "", "", ""
		}
		return asset.Renders[index], "image/png", ""
	}
	return "", "", ""
}

func quoteETag(hash string) string {
	if hash == "" {
		return ""
	}
	return `"` + hash + `"`
}

func ifNoneMatch(r *http.Request) string {
	return strings.TrimSpace(r.Header.Get("If-None-Match"))
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}
