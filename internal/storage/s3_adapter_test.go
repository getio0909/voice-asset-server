package storage

import (
	"bytes"
	"context"
	"encoding/xml"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
)

type s3HTTPTestServer struct {
	mu      sync.Mutex
	objects map[string]s3HTTPTestObject
	version int
}

type s3HTTPTestObject struct {
	data []byte
	etag string
}

func newS3HTTPTestServer() *s3HTTPTestServer {
	return &s3HTTPTestServer{objects: make(map[string]s3HTTPTestObject)}
}

func (server *s3HTTPTestServer) ServeHTTP(response http.ResponseWriter, request *http.Request) {
	if request.URL.Query().Get("list-type") == "2" {
		server.list(response, request)
		return
	}
	key, ok := s3HTTPKey(request.URL)
	if !ok {
		response.WriteHeader(http.StatusBadRequest)
		return
	}
	switch request.Method {
	case http.MethodPut:
		server.put(response, request, key)
	case http.MethodGet:
		server.get(response, key)
	case http.MethodDelete:
		server.delete(response, request, key)
	default:
		response.WriteHeader(http.StatusMethodNotAllowed)
	}
}

func (server *s3HTTPTestServer) put(response http.ResponseWriter, request *http.Request, key string) {
	data, err := io.ReadAll(request.Body)
	if err != nil {
		response.WriteHeader(http.StatusBadRequest)
		return
	}
	server.mu.Lock()
	defer server.mu.Unlock()
	if request.Header.Get("If-None-Match") == "*" {
		if _, exists := server.objects[key]; exists {
			response.WriteHeader(http.StatusPreconditionFailed)
			return
		}
	}
	server.version++
	etag := strconv.Itoa(server.version)
	server.objects[key] = s3HTTPTestObject{data: append([]byte(nil), data...), etag: `"` + etag + `"`}
	response.Header().Set("ETag", `"`+etag+`"`)
	response.WriteHeader(http.StatusOK)
}

func (server *s3HTTPTestServer) get(response http.ResponseWriter, key string) {
	server.mu.Lock()
	object, exists := server.objects[key]
	server.mu.Unlock()
	if !exists {
		response.WriteHeader(http.StatusNotFound)
		return
	}
	response.Header().Set("ETag", object.etag)
	response.Header().Set("Content-Length", strconv.Itoa(len(object.data)))
	_, _ = response.Write(object.data)
}

func (server *s3HTTPTestServer) delete(response http.ResponseWriter, request *http.Request, key string) {
	server.mu.Lock()
	defer server.mu.Unlock()
	object, exists := server.objects[key]
	if !exists {
		response.WriteHeader(http.StatusNotFound)
		return
	}
	if match := request.Header.Get("If-Match"); match != "*" && match != object.etag {
		response.WriteHeader(http.StatusPreconditionFailed)
		return
	}
	delete(server.objects, key)
	response.WriteHeader(http.StatusNoContent)
}

func (server *s3HTTPTestServer) list(response http.ResponseWriter, request *http.Request) {
	prefix := request.URL.Query().Get("prefix")
	server.mu.Lock()
	keys := make([]string, 0, len(server.objects))
	for key := range server.objects {
		if strings.HasPrefix(key, prefix) {
			keys = append(keys, key)
		}
	}
	server.mu.Unlock()
	sort.Strings(keys)
	start := request.URL.Query().Get("continuation-token")
	if start != "" {
		for index, key := range keys {
			if key == start {
				keys = keys[index+1:]
				break
			}
		}
	}
	maxKeys, _ := strconv.Atoi(request.URL.Query().Get("max-keys"))
	if maxKeys <= 0 {
		maxKeys = 1000
	}
	next := ""
	if len(keys) > maxKeys {
		next = keys[maxKeys-1]
		keys = keys[:maxKeys]
	}
	type listContents struct {
		Key string `xml:"Key"`
	}
	type listResult struct {
		XMLName          xml.Name       `xml:"ListBucketResult"`
		Name             string         `xml:"Name"`
		Prefix           string         `xml:"Prefix"`
		KeyCount         int            `xml:"KeyCount"`
		MaxKeys          int            `xml:"MaxKeys"`
		IsTruncated      bool           `xml:"IsTruncated"`
		NextContinuation string         `xml:"NextContinuationToken,omitempty"`
		Contents         []listContents `xml:"Contents"`
	}
	result := listResult{Name: "voice-assets-test", Prefix: prefix, KeyCount: len(keys), MaxKeys: maxKeys, IsTruncated: next != "", NextContinuation: next}
	for _, key := range keys {
		result.Contents = append(result.Contents, listContents{Key: key})
	}
	response.Header().Set("Content-Type", "application/xml")
	_ = xml.NewEncoder(response).Encode(result)
}

func s3HTTPKey(value *url.URL) (string, bool) {
	segments := strings.Split(strings.TrimPrefix(value.Path, "/"), "/")
	if len(segments) < 2 || segments[0] == "" {
		return "", false
	}
	key, err := url.PathUnescape(strings.Join(segments[1:], "/"))
	return key, err == nil && key != ""
}

func TestNewS3UsesAWSCompatibleAdapterForImmutableLifecycle(t *testing.T) {
	backend := newS3HTTPTestServer()
	server := httptest.NewServer(backend)
	t.Cleanup(server.Close)
	store, err := NewS3(S3Config{
		Endpoint:        server.URL,
		Region:          "test-1",
		Bucket:          "voice-assets-test",
		Prefix:          "tenant/test",
		AccessKeyID:     "test-access",
		SecretAccessKey: "test-secret",
		ForcePathStyle:  true,
		TempRoot:        t.TempDir(),
	})
	if err != nil {
		t.Fatalf("NewS3() error = %v", err)
	}
	content := []byte("SDK-backed immutable object")
	object, err := store.PutImmutable(context.Background(), "asset-adapter", "artifact-adapter", ObjectKindExport, bytes.NewReader(content), 1024)
	if err != nil {
		t.Fatalf("PutImmutable() error = %v", err)
	}
	retry, err := store.PutImmutable(context.Background(), "asset-adapter", "artifact-adapter", ObjectKindExport, bytes.NewReader(content), 1024)
	if err != nil || !retry.Reused {
		t.Fatalf("idempotent PutImmutable() = %#v, error = %v", retry, err)
	}
	file, err := store.Open(context.Background(), object.Key)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	read, err := io.ReadAll(file)
	closeErr := file.Close()
	if err != nil || closeErr != nil || !bytes.Equal(read, content) {
		t.Fatalf("Open() read = %q, read error = %v, close error = %v", read, err, closeErr)
	}
	if err := store.DeleteObject(context.Background(), object.Key, object.Size, object.SHA256); err != nil {
		t.Fatalf("DeleteObject() error = %v", err)
	}
	if _, err := store.Open(context.Background(), object.Key); !errors.Is(err, errS3NotFound) {
		t.Fatalf("Open(deleted) error = %v, want not found", err)
	}
}

func TestNewS3AdapterListsAndDeletesUploadParts(t *testing.T) {
	backend := newS3HTTPTestServer()
	server := httptest.NewServer(backend)
	t.Cleanup(server.Close)
	store, err := NewS3(S3Config{Endpoint: server.URL, Region: "test-1", Bucket: "voice-assets-test", ForcePathStyle: true, TempRoot: t.TempDir(), AccessKeyID: "test-access", SecretAccessKey: "test-secret"})
	if err != nil {
		t.Fatalf("NewS3() error = %v", err)
	}
	for number := 1; number <= 2; number++ {
		content := []byte("part-" + strconv.Itoa(number))
		if _, err := store.PutPart(context.Background(), "adapter-upload", number, bytes.NewReader(content), PutPartOptions{ExpectedSize: int64(len(content)), ExpectedSHA256: testSHA256(content), MaxBytes: 64}); err != nil {
			t.Fatalf("PutPart(%d) error = %v", number, err)
		}
	}
	if err := store.DeleteParts(context.Background(), "adapter-upload"); err != nil {
		t.Fatalf("DeleteParts() error = %v", err)
	}
	backend.mu.Lock()
	defer backend.mu.Unlock()
	if len(backend.objects) != 0 {
		t.Fatalf("remaining objects after DeleteParts = %d", len(backend.objects))
	}
}
