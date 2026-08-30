package main

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
)

// fakeObjectSource：内存对象源，支持 Range（bytes=a-b / a-）。
type fakeObjectSource struct {
	objects map[string][]byte
}

func (f *fakeObjectSource) Get(_ context.Context, key, rangeHeader string) (*ObjectData, error) {
	body, ok := f.objects[key]
	if !ok {
		return nil, ErrObjectNotFound
	}
	if rangeHeader == "" {
		return &ObjectData{
			Body:          io.NopCloser(strings.NewReader(string(body))),
			StatusCode:    200,
			ContentLength: int64(len(body)),
			ETag:          fmt.Sprintf(`"fake-%d"`, len(body)),
		}, nil
	}
	spec := strings.TrimPrefix(rangeHeader, "bytes=")
	parts := strings.SplitN(spec, "-", 2)
	start, err := strconv.Atoi(parts[0])
	if err != nil || start >= len(body) {
		return nil, ErrInvalidRange
	}
	end := len(body) - 1
	if parts[1] != "" {
		if end, err = strconv.Atoi(parts[1]); err != nil || end >= len(body) {
			return nil, ErrInvalidRange
		}
	}
	slice := body[start : end+1]
	return &ObjectData{
		Body:          io.NopCloser(strings.NewReader(string(slice))),
		StatusCode:    206,
		ContentLength: int64(len(slice)),
		ContentRange:  fmt.Sprintf("bytes %d-%d/%d", start, end, len(body)),
		ETag:          fmt.Sprintf(`"fake-%d"`, len(body)),
	}, nil
}

func newTestServer() (*Server, *fakeObjectSource) {
	glb := []byte("GLB-BYTES-0123456789")
	src := &fakeObjectSource{objects: map[string][]byte{
		"console/c1/t1/convert/a1.glb":      glb,
		"console/c1/t1/render/a1-front.png": []byte("PNG-FRONT"),
		"console/c1/t1/thumb/a1.png":        []byte("PNG-THUMB"),
	}}
	store := MapStore{
		"a1": {
			GLBPath:   "console/c1/t1/convert/a1.glb",
			GLBHash:   "sha256-abc",
			Thumbnail: "console/c1/t1/thumb/a1.png",
			Renders:   []string{"console/c1/t1/render/a1-front.png"},
		},
		"a2": {Renders: []string{}}, // 尚未就绪的资产
	}
	return NewServer(store, src, "public, max-age=31536000, immutable"), src
}

func doGet(t *testing.T, s *Server, path string, headers map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, path, nil)
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, req)
	return rec
}

func TestHealthz(t *testing.T) {
	s, _ := newTestServer()
	rec := doGet(t, s, "/healthz", nil)
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), "ok") {
		t.Fatalf("healthz = %d %s", rec.Code, rec.Body.String())
	}
}

func TestGLBRoundtripAndHeaders(t *testing.T) {
	s, _ := newTestServer()
	rec := doGet(t, s, "/assets/a1/glb", nil)
	if rec.Code != 200 {
		t.Fatalf("status = %d", rec.Code)
	}
	if rec.Body.String() != "GLB-BYTES-0123456789" {
		t.Fatalf("body mismatch: %q", rec.Body.String())
	}
	if got := rec.Header().Get("ETag"); got != `"sha256-abc"` {
		t.Fatalf("etag = %q", got)
	}
	if got := rec.Header().Get("Content-Type"); got != "model/gltf-binary" {
		t.Fatalf("content-type = %q", got)
	}
	if got := rec.Header().Get("Cache-Control"); !strings.Contains(got, "immutable") {
		t.Fatalf("cache-control = %q", got)
	}
	if rec.Header().Get("Accept-Ranges") != "bytes" {
		t.Fatal("missing Accept-Ranges")
	}
}

func TestRangeRequest(t *testing.T) {
	s, _ := newTestServer()
	rec := doGet(t, s, "/assets/a1/glb", map[string]string{"Range": "bytes=0-2"})
	if rec.Code != 206 {
		t.Fatalf("status = %d", rec.Code)
	}
	if rec.Body.String() != "GLB" {
		t.Fatalf("partial body = %q", rec.Body.String())
	}
	if got := rec.Header().Get("Content-Range"); got != "bytes 0-2/20" {
		t.Fatalf("content-range = %q", got)
	}
}

func TestIfNoneMatchNotModified(t *testing.T) {
	s, _ := newTestServer()
	rec := doGet(t, s, "/assets/a1/glb", map[string]string{"If-None-Match": `"sha256-abc"`})
	if rec.Code != 304 {
		t.Fatalf("status = %d", rec.Code)
	}
}

func TestThumbnailAndRenders(t *testing.T) {
	s, _ := newTestServer()
	if rec := doGet(t, s, "/assets/a1/thumbnail", nil); rec.Code != 200 || rec.Body.String() != "PNG-THUMB" {
		t.Fatalf("thumbnail = %d %q", rec.Code, rec.Body.String())
	}
	if rec := doGet(t, s, "/assets/a1/renders/0", nil); rec.Code != 200 || rec.Body.String() != "PNG-FRONT" {
		t.Fatalf("renders/0 = %d %q", rec.Code, rec.Body.String())
	}
	if rec := doGet(t, s, "/assets/a1/renders/3", nil); rec.Code != 404 {
		t.Fatalf("renders out-of-bounds = %d", rec.Code)
	}
}

func TestNotFoundPaths(t *testing.T) {
	s, _ := newTestServer()
	for _, path := range []string{
		"/assets/nope/glb",     // 资产不存在
		"/assets/a2/glb",       // 产物未就绪
		"/assets/a1/unknown",   // 非法产物类型
		"/assets/a1/renders/x", // 非法索引
	} {
		if rec := doGet(t, s, path, nil); rec.Code != 404 {
			t.Fatalf("%s = %d, want 404", path, rec.Code)
		}
	}
}

func TestObjectMissingInStore(t *testing.T) {
	s, src := newTestServer()
	delete(src.objects, "console/c1/t1/convert/a1.glb")
	if rec := doGet(t, s, "/assets/a1/glb", nil); rec.Code != 404 {
		t.Fatalf("missing object = %d, want 404", rec.Code)
	}
}
