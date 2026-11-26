package s3storage

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"mediahub_oss/internal/shared/customerrors"
	"mediahub_oss/internal/shared/ulid"
	"mediahub_oss/internal/storage"
)

func TestGetObjectKey(t *testing.T) {
	id1 := ulid.MustParse("01HGFB9Z5W7ABCDEFGHJKMNPQR")
	id2 := ulid.MustParse("01J2A3X9D4B5C6E7F8G9H0J1K2")
	tests := []struct {
		dbID     ulid.ULID
		id       int64
		expected string
	}{
		{
			dbID:     id1,
			id:       0,
			expected: "01HGFB9Z5W7ABCDEFGHJKMNPQR/0/0",
		},
		{
			dbID:     id1,
			id:       10232,
			expected: "01HGFB9Z5W7ABCDEFGHJKMNPQR/10/10232",
		},
		{
			dbID:     id2,
			id:       999,
			expected: "01J2A3X9D4B5C6E7F8G9H0J1K2/0/999",
		},
		{
			dbID:     id2,
			id:       1000,
			expected: "01J2A3X9D4B5C6E7F8G9H0J1K2/1/1000",
		},
	}

	for _, tt := range tests {
		got := getObjectKey(tt.dbID, tt.id)
		if got != tt.expected {
			t.Errorf("getObjectKey(%s, %d) = %s; want %s", tt.dbID, tt.id, got, tt.expected)
		}
	}
}

func TestGetPreviewObjectKey(t *testing.T) {
	tests := []struct {
		dbID     ulid.ULID
		id       int64
		expected string
	}{
		{
			dbID:     ulid.MustParse("01HGFB9Z5W7ABCDEFGHJKMNPQR"),
			id:       10232,
			expected: "previews/01HGFB9Z5W7ABCDEFGHJKMNPQR/10/10232",
		},
	}

	for _, tt := range tests {
		got := getPreviewObjectKey(tt.dbID, tt.id)
		if got != tt.expected {
			t.Errorf("getPreviewObjectKey(%s, %d) = %s; want %s", tt.dbID, tt.id, got, tt.expected)
		}
	}
}

func TestNewS3StorageProviderValidation(t *testing.T) {
	invalidConfigs := []Config{
		{},
		{Endpoint: "localhost:9000"},
		{Endpoint: "localhost:9000", Bucket: "testbucket"},
		{Endpoint: "localhost:9000", Bucket: "testbucket", AccessKey: "access"},
	}

	for i, cfg := range invalidConfigs {
		_, err := NewS3StorageProvider(cfg)
		if err == nil {
			t.Errorf("case %d: expected error for invalid config, got nil", i)
		}
	}
}

// Simple in-memory S3 mock server to test operations
type mockS3Server struct {
	mu           sync.RWMutex
	objects      map[string][]byte
	headRequests int
}

func newMockS3Server() (*httptest.Server, *mockS3Server) {
	mock := &mockS3Server{
		objects: make(map[string][]byte),
	}

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mock.mu.Lock()
		defer mock.mu.Unlock()

		if r.URL.Path == "/testbucket" || r.URL.Path == "/testbucket/" {
			if r.Method == http.MethodHead {
				w.Header().Set("x-amz-bucket-region", "us-east-1")
				w.WriteHeader(http.StatusOK)
				return
			}
			if r.URL.Query().Has("location") {
				w.Header().Set("Content-Type", "application/xml")
				w.WriteHeader(http.StatusOK)
				w.Write([]byte(`<?xml version="1.0" encoding="UTF-8"?><LocationConstraint xmlns="http://s3.amazonaws.com/doc/2006-03-01/">us-east-1</LocationConstraint>`))
				return
			}
		}

		key := strings.TrimPrefix(r.URL.Path, "/testbucket/")

		switch r.Method {
		case http.MethodPut:
			data, err := io.ReadAll(r.Body)
			if err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
			mock.objects[key] = data
			w.Header().Set("ETag", `"mocketag"`)
			w.WriteHeader(http.StatusOK)

		case http.MethodHead:
			mock.headRequests++
			data, ok := mock.objects[key]
			if !ok {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			w.Header().Set("Content-Length", strconv.Itoa(len(data)))
			w.Header().Set("Last-Modified", time.Now().UTC().Format(http.TimeFormat))
			w.WriteHeader(http.StatusOK)

		case http.MethodGet:
			query := r.URL.Query()
			// Handle ListObjectsV2
			if query.Get("list-type") == "2" || r.URL.RawQuery != "" && strings.Contains(r.URL.RawQuery, "list-type=2") {
				prefix := query.Get("prefix")
				var keys []string
				for k := range mock.objects {
					if strings.HasPrefix(k, prefix) {
						keys = append(keys, k)
					}
				}
				w.Header().Set("Content-Type", "application/xml")
				w.WriteHeader(http.StatusOK)
				xmlResp := `<?xml version="1.0" encoding="UTF-8"?>
<ListBucketResult xmlns="http://s3.amazonaws.com/doc/2006-03-01/">
    <Name>testbucket</Name>
    <Prefix>` + prefix + `</Prefix>
    <KeyCount>` + strconv.Itoa(len(keys)) + `</KeyCount>
    <IsTruncated>false</IsTruncated>`
				for _, k := range keys {
					data := mock.objects[k]
					xmlResp += `<Contents>
        <Key>` + k + `</Key>
        <Size>` + strconv.Itoa(len(data)) + `</Size>
    </Contents>`
				}
				xmlResp += `</ListBucketResult>`
				w.Write([]byte(xmlResp))
				return
			}

			data, ok := mock.objects[key]
			if !ok {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			rangeHeader := r.Header.Get("Range")
			if strings.HasPrefix(rangeHeader, "bytes=") {
				parts := strings.Split(strings.TrimPrefix(rangeHeader, "bytes="), "-")
				start, _ := strconv.ParseInt(parts[0], 10, 64)
				total := len(data)
				end := int64(total) - 1
				if len(parts) > 1 && parts[1] != "" {
					if parsedEnd, err := strconv.ParseInt(parts[1], 10, 64); err == nil {
						end = parsedEnd
					}
				}
				if start < int64(total) {
					if end >= int64(total) {
						end = int64(total) - 1
					}
					data = data[start : end+1]
				}
				w.Header().Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", start, end, total))
				w.Header().Set("Content-Length", strconv.Itoa(len(data)))
				w.Header().Set("Last-Modified", time.Now().UTC().Format(http.TimeFormat))
				w.WriteHeader(http.StatusPartialContent)
				w.Write(data)
				return
			}
			w.Header().Set("Content-Length", strconv.Itoa(len(data)))
			w.Header().Set("Last-Modified", time.Now().UTC().Format(http.TimeFormat))
			w.WriteHeader(http.StatusOK)
			w.Write(data)

		case http.MethodDelete:
			delete(mock.objects, key)
			w.WriteHeader(http.StatusNoContent)

		case http.MethodPost:
			query := r.URL.Query()
			if query.Has("uploads") {
				w.Header().Set("Content-Type", "application/xml")
				w.WriteHeader(http.StatusOK)
				w.Write([]byte(`<?xml version="1.0" encoding="UTF-8"?><InitiateMultipartUploadResult><Bucket>testbucket</Bucket><Key>` + key + `</Key><UploadId>mock-upload-id</UploadId></InitiateMultipartUploadResult>`))
				return
			}
			if query.Has("uploadId") {
				w.Header().Set("Content-Type", "application/xml")
				w.WriteHeader(http.StatusOK)
				w.Write([]byte(`<?xml version="1.0" encoding="UTF-8"?><CompleteMultipartUploadResult><Location>http://localhost/testbucket/` + key + `</Location><Bucket>testbucket</Bucket><Key>` + key + `</Key><ETag>"mocketag"</ETag></CompleteMultipartUploadResult>`))
				return
			}
			// Handle delete objects batch
			if query.Has("delete") {
				bodyBytes, _ := io.ReadAll(r.Body)
				bodyStr := string(bodyBytes)
				for k := range mock.objects {
					if strings.Contains(bodyStr, k) {
						delete(mock.objects, k)
					}
				}
				w.Header().Set("Content-Type", "application/xml")
				w.WriteHeader(http.StatusOK)
				w.Write([]byte(`<?xml version="1.0" encoding="UTF-8"?><DeleteResult></DeleteResult>`))
				return
			}
			w.WriteHeader(http.StatusOK)

		default:
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
	})

	server := httptest.NewServer(handler)
	return server, mock
}

func TestS3StorageProviderOperations(t *testing.T) {
	ts, mock := newMockS3Server()
	defer ts.Close()

	u, err := url.Parse(ts.URL)
	if err != nil {
		t.Fatalf("failed to parse test server url: %v", err)
	}

	provider, err := NewS3StorageProvider(Config{
		Endpoint:  u.Host,
		Region:    "us-east-1",
		Bucket:    "testbucket",
		AccessKey: "mockaccesskey",
		SecretKey: "mocksecretkey",
		UseSSL:    false,
	})
	if err != nil {
		t.Fatalf("NewS3StorageProvider failed: %v", err)
	}

	ctx := context.Background()
	dbID := ulid.MustParse("01HGFB9Z5W7ABCDEFGHJKMNPQR")
	var entryID int64 = 10232

	// 1. Write file
	content := []byte("hello s3 storage content")
	written, err := provider.Write(ctx, dbID, entryID, bytes.NewReader(content))
	if err != nil {
		t.Fatalf("Write failed: %v", err)
	}
	if written != int64(len(content)) {
		t.Errorf("written = %d; want %d", written, len(content))
	}

	// 2. Stat file
	stat, err := provider.Stat(ctx, dbID, entryID)
	if err != nil {
		t.Fatalf("Stat failed: %v", err)
	}
	if stat.Size != int64(len(content)) {
		t.Errorf("stat.Size = %d; want %d", stat.Size, len(content))
	}

	// 3. Stat non-existent file
	_, err = provider.Stat(ctx, dbID, 99999)
	if err != customerrors.ErrNotFound {
		t.Errorf("expected customerrors.ErrNotFound, got: %v", err)
	}

	// 4. Read file (verify no HEAD request is issued during Read)
	mock.mu.RLock()
	headBefore := mock.headRequests
	mock.mu.RUnlock()

	rc, err := provider.Read(ctx, dbID, entryID, 0, -1)
	if err != nil {
		t.Fatalf("Read failed: %v", err)
	}
	readBytes, err := io.ReadAll(rc)
	rc.Close()
	if err != nil {
		t.Fatalf("ReadAll failed: %v", err)
	}
	if !bytes.Equal(readBytes, content) {
		t.Errorf("Read content = %s; want %s", string(readBytes), string(content))
	}

	// 5. Read with offset and length
	rcRange, err := provider.Read(ctx, dbID, entryID, 6, 2)
	if err != nil {
		t.Fatalf("Read with range failed: %v", err)
	}
	rangeBytes, err := io.ReadAll(rcRange)
	rcRange.Close()
	if err != nil {
		t.Fatalf("ReadAll range failed: %v", err)
	}
	if string(rangeBytes) != "s3" {
		t.Errorf("Read range content = %s; want 's3'", string(rangeBytes))
	}

	// 5b. Read non-existent file returns ErrNotFound without HEAD request
	_, err = provider.Read(ctx, dbID, 99999, 0, -1)
	if err != customerrors.ErrNotFound {
		t.Errorf("Read non-existent: expected customerrors.ErrNotFound, got: %v", err)
	}

	mock.mu.RLock()
	headAfter := mock.headRequests
	mock.mu.RUnlock()
	if headAfter != headBefore {
		t.Errorf("expected Read to issue 0 HEAD requests, but issued %d", headAfter-headBefore)
	}

	// 6. Write Preview & Stat Preview & Read Preview
	previewContent := []byte("mock webp preview data")
	_, err = provider.WritePreview(ctx, dbID, entryID, bytes.NewReader(previewContent))
	if err != nil {
		t.Fatalf("WritePreview failed: %v", err)
	}

	pStat, err := provider.StatPreview(ctx, dbID, entryID)
	if err != nil {
		t.Fatalf("StatPreview failed: %v", err)
	}
	if pStat.Size != int64(len(previewContent)) {
		t.Errorf("pStat.Size = %d; want %d", pStat.Size, len(previewContent))
	}

	pRc, err := provider.ReadPreview(ctx, dbID, entryID)
	if err != nil {
		t.Fatalf("ReadPreview failed: %v", err)
	}
	pBytes, _ := io.ReadAll(pRc)
	pRc.Close()
	if !bytes.Equal(pBytes, previewContent) {
		t.Errorf("ReadPreview content = %s; want %s", string(pBytes), string(previewContent))
	}

	// 7. Walk and WalkPreview
	var walkedMain []int64
	err = provider.Walk(ctx, dbID, func(id int64, info storage.FileInfo) error {
		walkedMain = append(walkedMain, id)
		return nil
	})
	if err != nil {
		t.Fatalf("Walk failed: %v", err)
	}
	if len(walkedMain) != 1 || walkedMain[0] != entryID {
		t.Errorf("Walk ids = %v; want [%d]", walkedMain, entryID)
	}

	var walkedPreview []int64
	err = provider.WalkPreview(ctx, dbID, func(id int64, info storage.FileInfo) error {
		walkedPreview = append(walkedPreview, id)
		return nil
	})
	if err != nil {
		t.Fatalf("WalkPreview failed: %v", err)
	}
	if len(walkedPreview) != 1 || walkedPreview[0] != entryID {
		t.Errorf("WalkPreview ids = %v; want [%d]", walkedPreview, entryID)
	}

	// 8. DeleteMultiple & DeleteMultiplePreviews
	bulkRes, err := provider.DeleteMultiple(ctx, dbID, []int64{entryID})
	if err != nil {
		t.Fatalf("DeleteMultiple failed: %v", err)
	}
	if len(bulkRes.Success) != 1 || bulkRes.Success[0] != entryID {
		t.Errorf("DeleteMultiple success = %v; want [%d]", bulkRes.Success, entryID)
	}

	bulkPRes, err := provider.DeleteMultiplePreviews(ctx, dbID, []int64{entryID})
	if err != nil {
		t.Fatalf("DeleteMultiplePreviews failed: %v", err)
	}
	if len(bulkPRes.Success) != 1 || bulkPRes.Success[0] != entryID {
		t.Errorf("DeleteMultiplePreviews success = %v; want [%d]", bulkPRes.Success, entryID)
	}

	// 9. Stat after delete should return ErrNotFound
	_, err = provider.Stat(ctx, dbID, entryID)
	if err != customerrors.ErrNotFound {
		t.Errorf("Stat after Delete: expected ErrNotFound, got: %v", err)
	}

	// 10. Test DeleteDatabase
	db2 := ulid.MustParse("01HGFB9Z5W7ABCDEFGHJKMNPQ2")
	_, err = provider.Write(ctx, db2, 1, bytes.NewReader([]byte("main file")))
	if err != nil {
		t.Fatalf("Write for DeleteDatabase test failed: %v", err)
	}
	_, err = provider.WritePreview(ctx, db2, 1, bytes.NewReader([]byte("preview file")))
	if err != nil {
		t.Fatalf("WritePreview for DeleteDatabase test failed: %v", err)
	}

	err = provider.DeleteDatabase(ctx, db2)
	if err != nil {
		t.Fatalf("DeleteDatabase failed: %v", err)
	}

	_, err = provider.Stat(ctx, db2, 1)
	if err != customerrors.ErrNotFound {
		t.Errorf("Stat after DeleteDatabase expected ErrNotFound, got: %v", err)
	}
	_, err = provider.StatPreview(ctx, db2, 1)
	if err != customerrors.ErrNotFound {
		t.Errorf("StatPreview after DeleteDatabase expected ErrNotFound, got: %v", err)
	}
}

func TestS3DeleteMultipleErrors(t *testing.T) {
	// Mock server that returns custom DeleteResult XML with errors
	var deleteXMLResponse string
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/testbucket" || r.URL.Path == "/testbucket/" {
			if r.Method == http.MethodHead {
				w.Header().Set("x-amz-bucket-region", "us-east-1")
				w.WriteHeader(http.StatusOK)
				return
			}
			if r.URL.Query().Has("location") {
				w.Header().Set("Content-Type", "application/xml")
				w.WriteHeader(http.StatusOK)
				w.Write([]byte(`<?xml version="1.0" encoding="UTF-8"?><LocationConstraint xmlns="http://s3.amazonaws.com/doc/2006-03-01/">us-east-1</LocationConstraint>`))
				return
			}
		}

		if r.Method == http.MethodPost && r.URL.Query().Has("delete") {
			w.Header().Set("Content-Type", "application/xml")
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(deleteXMLResponse))
			return
		}

		w.WriteHeader(http.StatusOK)
	})

	ts := httptest.NewServer(handler)
	defer ts.Close()

	u, err := url.Parse(ts.URL)
	if err != nil {
		t.Fatalf("failed to parse test server url: %v", err)
	}

	provider, err := NewS3StorageProvider(Config{
		Endpoint:  u.Host,
		Region:    "us-east-1",
		Bucket:    "testbucket",
		AccessKey: "mockaccesskey",
		SecretKey: "mocksecretkey",
		UseSSL:    false,
	})
	if err != nil {
		t.Fatalf("NewS3StorageProvider failed: %v", err)
	}

	ctx := context.Background()
	dbID := ulid.MustParse("01HGFB9Z5W7ABCDEFGHJKMNPQR")

	// Case 1: Mapped error on ID 1
	deleteXMLResponse = `<?xml version="1.0" encoding="UTF-8"?>
<DeleteResult xmlns="http://s3.amazonaws.com/doc/2006-03-01/">
    <Error>
        <Key>01HGFB9Z5W7ABCDEFGHJKMNPQR/0/1</Key>
        <Code>AccessDenied</Code>
        <Message>Access Denied</Message>
    </Error>
</DeleteResult>`

	res, err := provider.DeleteMultiple(ctx, dbID, []int64{1, 2})
	if err == nil {
		t.Errorf("expected error for mapped deletion failure, got nil")
	}
	if len(res.Failed) != 1 || res.Failed[0] != 1 {
		t.Errorf("expected Failed=[1], got: %v", res.Failed)
	}
	if len(res.Success) != 1 || res.Success[0] != 2 {
		t.Errorf("expected Success=[2], got: %v", res.Success)
	}

	// Case 2: Unmapped / General error (no Key in Error element)
	deleteXMLResponse = `<?xml version="1.0" encoding="UTF-8"?>
<DeleteResult xmlns="http://s3.amazonaws.com/doc/2006-03-01/">
    <Error>
        <Code>InternalError</Code>
        <Message>General error</Message>
    </Error>
</DeleteResult>`

	res, err = provider.DeleteMultiple(ctx, dbID, []int64{1, 2})
	if err == nil {
		t.Errorf("expected error for unmapped deletion failure, got nil")
	}
	if len(res.Failed) != 2 {
		t.Errorf("expected Failed=[1, 2] for unmapped error, got: %v", res.Failed)
	}
	if len(res.Success) != 0 {
		t.Errorf("expected Success=[] for unmapped error, got: %v", res.Success)
	}
}

func TestS3StorageProvider_DeleteDatabase(t *testing.T) {
	ts, _ := newMockS3Server()
	defer ts.Close()

	u, _ := url.Parse(ts.URL)
	cfg := Config{
		Endpoint:  u.Host,
		Bucket:    "testbucket",
		AccessKey: "minioadmin",
		SecretKey: "minioadmin",
		UseSSL:    false,
		Region:    "us-east-1",
	}

	provider, err := NewS3StorageProvider(cfg)
	if err != nil {
		t.Fatalf("failed to create s3 provider: %v", err)
	}

	ctx := context.Background()
	dbID := ulid.MustParse("01HGFB9Z5W7ABCDEFGHJKMNPQR")

	// Write objects and previews
	for id := int64(1); id <= 3; id++ {
		data := []byte(fmt.Sprintf("data-%d", id))
		if _, err := provider.Write(ctx, dbID, id, bytes.NewReader(data)); err != nil {
			t.Fatalf("failed to write object %d: %v", id, err)
		}
		if _, err := provider.WritePreview(ctx, dbID, id, bytes.NewReader(data)); err != nil {
			t.Fatalf("failed to write preview %d: %v", id, err)
		}
	}

	// DeleteDatabase
	if err := provider.DeleteDatabase(ctx, dbID); err != nil {
		t.Fatalf("unexpected error during DeleteDatabase: %v", err)
	}

	// Verify all are removed
	for id := int64(1); id <= 3; id++ {
		if _, err := provider.Read(ctx, dbID, id, 0, -1); err == nil {
			t.Errorf("expected object %d to be deleted, but read succeeded", id)
		}
		if _, err := provider.ReadPreview(ctx, dbID, id); err == nil {
			t.Errorf("expected preview %d to be deleted, but read succeeded", id)
		}
	}
}

func TestS3StorageProvider_ZeroULIDDeleteDatabaseLeavesSiblingsUntouched(t *testing.T) {
	ts, mock := newMockS3Server()
	defer ts.Close()

	u, err := url.Parse(ts.URL)
	if err != nil {
		t.Fatalf("failed to parse test server url: %v", err)
	}

	provider, err := NewS3StorageProvider(Config{
		Endpoint:  u.Host,
		Region:    "us-east-1",
		Bucket:    "testbucket",
		AccessKey: "mockaccesskey",
		SecretKey: "mocksecretkey",
		UseSSL:    false,
	})
	if err != nil {
		t.Fatalf("NewS3StorageProvider failed: %v", err)
	}

	ctx := context.Background()
	validDB := ulid.MustParse("01HGFB9Z5W7ABCDEFGHJKMNPQR")
	if _, err := provider.Write(ctx, validDB, 1, bytes.NewReader([]byte("main"))); err != nil {
		t.Fatalf("setup Write failed: %v", err)
	}
	if _, err := provider.WritePreview(ctx, validDB, 1, bytes.NewReader([]byte("preview"))); err != nil {
		t.Fatalf("setup WritePreview failed: %v", err)
	}

	if err := provider.DeleteDatabase(ctx, ulid.ULID{}); err != nil {
		t.Fatalf("DeleteDatabase(zero ULID) failed: %v", err)
	}

	mock.mu.RLock()
	_, mainExists := mock.objects[getObjectKey(validDB, 1)]
	_, previewExists := mock.objects[getPreviewObjectKey(validDB, 1)]
	mock.mu.RUnlock()

	if !mainExists {
		t.Fatalf("expected valid main object to remain intact")
	}
	if !previewExists {
		t.Fatalf("expected valid preview object to remain intact")
	}
}

func TestS3StorageProvider_ReadZeroLengthAndNegativeOffset(t *testing.T) {
	validDB := ulid.MustParse("01HGFB9Z5W7ABCDEFGHJKMNPQR")

	t.Run("negative offset returns ErrValidation without network", func(t *testing.T) {
		provider, err := NewS3StorageProvider(Config{
			Endpoint:  "localhost:9000",
			Region:    "us-east-1",
			Bucket:    "testbucket",
			AccessKey: "mockaccesskey",
			SecretKey: "mocksecretkey",
		})
		if err != nil {
			t.Fatalf("NewS3StorageProvider failed: %v", err)
		}

		if _, err := provider.Read(context.Background(), validDB, 1, -1, 5); !errors.Is(err, customerrors.ErrValidation) {
			t.Fatalf("expected ErrValidation for negative offset, got %v", err)
		}
	})

	t.Run("length zero returns empty reader without invalid range header", func(t *testing.T) {
		var sawRangeZeroMinusOne bool
		ts, mock := newMockS3Server()
		wrapped := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Header.Get("Range") == "bytes=0--1" || strings.Contains(r.Header.Get("Range"), "--") {
				sawRangeZeroMinusOne = true
			}
			ts.Config.Handler.ServeHTTP(w, r)
		}))
		defer wrapped.Close()
		defer ts.Close()

		u, err := url.Parse(wrapped.URL)
		if err != nil {
			t.Fatalf("failed to parse URL: %v", err)
		}
		provider, err := NewS3StorageProvider(Config{
			Endpoint:  u.Host,
			Region:    "us-east-1",
			Bucket:    "testbucket",
			AccessKey: "mockaccesskey",
			SecretKey: "mocksecretkey",
			UseSSL:    false,
		})
		if err != nil {
			t.Fatalf("NewS3StorageProvider failed: %v", err)
		}

		ctx := context.Background()
		const entryID = int64(10)
		if _, err := provider.Write(ctx, validDB, entryID, bytes.NewReader([]byte("hello s3"))); err != nil {
			t.Fatalf("Write failed: %v", err)
		}

		for _, offset := range []int64{0, 5} {
			rc, err := provider.Read(ctx, validDB, entryID, offset, 0)
			if err != nil {
				t.Fatalf("Read(offset=%d, length=0) failed: %v", offset, err)
			}
			data, readErr := io.ReadAll(rc)
			rc.Close()
			if readErr != nil {
				t.Fatalf("ReadAll(offset=%d, length=0) failed: %v", offset, readErr)
			}
			if len(data) != 0 {
				t.Fatalf("expected empty slice for length=0, got %d bytes", len(data))
			}
		}

		if _, err := provider.Read(ctx, validDB, 99999, 0, 0); !errors.Is(err, customerrors.ErrNotFound) {
			t.Fatalf("expected ErrNotFound for missing object with length=0, got %v", err)
		}

		mock.mu.RLock()
		heads := mock.headRequests
		mock.mu.RUnlock()
		if heads < 3 {
			t.Errorf("expected Stat HEAD checks for length=0 reads, got %d", heads)
		}
		if sawRangeZeroMinusOne {
			t.Fatal("observed invalid negative Range header")
		}
	})
}

func TestS3StorageProvider_DeleteDatabaseCanceled(t *testing.T) {
	validDB := ulid.MustParse("01HGFB9Z5W7ABCDEFGHJKMNPQR")

	t.Run("pre-canceled context returns context.Canceled", func(t *testing.T) {
		provider, err := NewS3StorageProvider(Config{
			Endpoint:  "localhost:9000",
			Region:    "us-east-1",
			Bucket:    "testbucket",
			AccessKey: "mockaccesskey",
			SecretKey: "mocksecretkey",
		})
		if err != nil {
			t.Fatalf("NewS3StorageProvider failed: %v", err)
		}

		ctx, cancel := context.WithCancel(context.Background())
		cancel()

		if err := provider.DeleteDatabase(ctx, validDB); !errors.Is(err, context.Canceled) {
			t.Fatalf("expected context.Canceled, got %v", err)
		}
	})

	t.Run("mid-operation cancellation returns context.Canceled", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		releaseHandler := make(chan struct{})

		handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method == http.MethodGet && r.URL.Query().Get("list-type") == "2" {
				w.Header().Set("Content-Type", "application/xml")
				w.WriteHeader(http.StatusOK)
				w.Write([]byte(`<?xml version="1.0" encoding="UTF-8"?>
<ListBucketResult xmlns="http://s3.amazonaws.com/doc/2006-03-01/">
    <Name>testbucket</Name>
    <Prefix>` + r.URL.Query().Get("prefix") + `</Prefix>
    <KeyCount>1</KeyCount>
    <IsTruncated>false</IsTruncated>
    <Contents><Key>` + validDB.String() + `/0/1</Key><Size>4</Size></Contents>
</ListBucketResult>`))
				return
			}
			if r.Method == http.MethodPost && r.URL.Query().Has("delete") {
				_, _ = io.Copy(io.Discard, r.Body)
				cancel()
				select {
				case <-r.Context().Done():
				case <-releaseHandler:
				}
				return
			}
			w.WriteHeader(http.StatusOK)
		})

		ts := httptest.NewServer(handler)
		defer ts.Close()
		defer close(releaseHandler)
		u, _ := url.Parse(ts.URL)

		provider, err := NewS3StorageProvider(Config{
			Endpoint:  u.Host,
			Region:    "us-east-1",
			Bucket:    "testbucket",
			AccessKey: "mockaccesskey",
			SecretKey: "mocksecretkey",
			UseSSL:    false,
		})
		if err != nil {
			t.Fatalf("NewS3StorageProvider failed: %v", err)
		}

		if err := provider.DeleteDatabase(ctx, validDB); !errors.Is(err, context.Canceled) {
			t.Fatalf("expected context.Canceled on canceled DeleteDatabase, got %v", err)
		}
	})
}

func TestS3StorageProvider_WalkEarlyExitCancelsListObjects(t *testing.T) {
	validDB := ulid.MustParse("01HGFB9Z5W7ABCDEFGHJKMNPQR")
	sentinelErr := errors.New("stop walk early")

	page2Done := make(chan bool, 1)
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Query().Get("list-type") != "2" {
			w.WriteHeader(http.StatusOK)
			return
		}
		if r.URL.Query().Get("continuation-token") == "page2" {
			select {
			case <-r.Context().Done():
				page2Done <- true
			case <-time.After(3 * time.Second):
				page2Done <- false
			}
			return
		}
		w.Header().Set("Content-Type", "application/xml")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`<?xml version="1.0" encoding="UTF-8"?>
<ListBucketResult xmlns="http://s3.amazonaws.com/doc/2006-03-01/">
    <Name>testbucket</Name>
    <Prefix>` + validDB.String() + `/</Prefix>
    <KeyCount>1</KeyCount>
    <IsTruncated>true</IsTruncated>
    <NextContinuationToken>page2</NextContinuationToken>
    <Contents><Key>` + validDB.String() + `/0/1</Key><Size>12</Size></Contents>
</ListBucketResult>`))
	})

	ts := httptest.NewServer(handler)
	defer ts.Close()
	u, _ := url.Parse(ts.URL)

	provider, err := NewS3StorageProvider(Config{
		Endpoint:  u.Host,
		Region:    "us-east-1",
		Bucket:    "testbucket",
		AccessKey: "mockaccesskey",
		SecretKey: "mocksecretkey",
		UseSSL:    false,
	})
	if err != nil {
		t.Fatalf("NewS3StorageProvider failed: %v", err)
	}

	err = provider.Walk(context.Background(), validDB, func(id int64, info storage.FileInfo) error {
		return sentinelErr
	})
	if !errors.Is(err, sentinelErr) {
		t.Fatalf("expected sentinelErr, got %v", err)
	}

	select {
	case canceled := <-page2Done:
		if !canceled {
			t.Fatal("expected ListObjects page 2 request to be canceled on Walk early exit")
		}
	case <-time.After(100 * time.Millisecond):
		// Also valid if ListObjects exited before initiating page 2.
	}
}

