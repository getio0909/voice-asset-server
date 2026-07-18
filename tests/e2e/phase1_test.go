package e2e_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/getio0909/voice-asset-server/internal/asr"
	"github.com/getio0909/voice-asset-server/internal/asset"
	"github.com/getio0909/voice-asset-server/internal/audio"
	"github.com/getio0909/voice-asset-server/internal/auth"
	"github.com/getio0909/voice-asset-server/internal/job"
	"github.com/getio0909/voice-asset-server/internal/platform/httpapi"
	"github.com/getio0909/voice-asset-server/internal/platform/migration"
	"github.com/getio0909/voice-asset-server/internal/storage"
	"github.com/getio0909/voice-asset-server/internal/transcript"
	"github.com/getio0909/voice-asset-server/internal/transcription"
	"github.com/getio0909/voice-asset-server/internal/upload"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestPhase1OwnerUploadMockTranscriptionAndPlayback(t *testing.T) {
	pool := migratedE2EPool(t)
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	t.Cleanup(cancel)
	authRepository := auth.NewPostgresRepository(pool)
	const (
		email    = "phase1-owner@example.com"
		password = "correct-horse-battery-staple"
	)
	if _, err := auth.NewBootstrapService(authRepository, auth.PasswordHasher{}).CreateOwner(ctx, auth.OwnerInput{
		Email: email, Password: password, WorkspaceName: "Phase 1 E2E",
	}); err != nil {
		t.Fatalf("bootstrap owner: %v", err)
	}

	localStorage, err := storage.NewLocal(t.TempDir())
	if err != nil {
		t.Fatalf("initialize local storage: %v", err)
	}
	authService := auth.NewService(authRepository, auth.PasswordHasher{})
	assetRepository := asset.NewPostgresRepository(pool)
	assetService := asset.NewService(assetRepository)
	uploadService := upload.NewService(upload.NewPostgresRepository(pool), localStorage)
	jobRepository := job.NewPostgresRepository(pool)
	jobService := job.NewService(jobRepository)
	transcriptService := transcript.NewService(transcript.NewPostgresRepository(pool))
	audioService := audio.NewAccessService(audio.NewPostgresOriginalRepository(pool), localStorage)

	server := httptest.NewUnstartedServer(nil)
	publicOrigin := "http://" + server.Listener.Addr().String()
	server.Config.Handler = httpapi.NewApplicationHandler(httpapi.Options{
		BrandName: "VoiceAsset", AuthService: authService, AssetService: assetService,
		UploadService: uploadService, JobService: jobService,
		TranscriptService: transcriptService, AudioService: audioService,
		PublicOrigin: publicOrigin, CookieSecure: false,
	})
	server.Start()
	t.Cleanup(server.Close)
	if server.URL != publicOrigin {
		t.Fatalf("test server origin = %q, configured %q", server.URL, publicOrigin)
	}
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatalf("create cookie jar: %v", err)
	}
	client := &http.Client{Transport: server.Client().Transport, Jar: jar, Timeout: 30 * time.Second}

	loginBody := fmt.Sprintf(`{"email":%q,"password":%q}`, email, password)
	loginResponse := request(t, client, http.MethodPost, server.URL+"/api/v1/auth/sessions",
		map[string]string{"Content-Type": "application/json", "Origin": publicOrigin},
		stringsReader(loginBody), http.StatusCreated)
	if bytes.Contains(loginResponse.body, []byte("access_token")) {
		t.Fatal("web login response exposed the access token")
	}
	setCookie := loginResponse.header.Get("Set-Cookie")
	if !bytes.Contains([]byte(setCookie), []byte("HttpOnly")) ||
		!bytes.Contains([]byte(setCookie), []byte("SameSite=Strict")) {
		t.Fatalf("session cookie missing security attributes: %q", setCookie)
	}

	assetResponse := request(t, client, http.MethodPost, server.URL+"/api/v1/assets",
		map[string]string{
			"Content-Type": "application/json", "Origin": publicOrigin,
			"Idempotency-Key": "e2e-create-asset",
		}, stringsReader(`{"title":"Phase 1 Recording","language":"en-US"}`), http.StatusCreated)
	var createdAsset asset.Asset
	decode(t, assetResponse.body, &createdAsset)

	wav := validTwoPartWAV()
	wavDigest := sha256.Sum256(wav)
	uploadDeclaration, err := json.Marshal(upload.CreateInput{
		AssetID: createdAsset.ID, Filename: "phase1.wav", MIMEType: "audio/wav",
		SizeBytes: int64(len(wav)), SHA256: hex.EncodeToString(wavDigest[:]),
	})
	if err != nil {
		t.Fatalf("encode upload declaration: %v", err)
	}
	uploadResponse := request(t, client, http.MethodPost, server.URL+"/api/v1/uploads",
		map[string]string{
			"Content-Type": "application/json", "Origin": publicOrigin,
			"Idempotency-Key": "e2e-upload",
		}, bytes.NewReader(uploadDeclaration), http.StatusCreated)
	var session upload.Session
	decode(t, uploadResponse.body, &session)
	if session.PartSize != upload.DefaultPartSize || len(wav) <= session.PartSize {
		t.Fatalf("upload fixture did not exercise two parts: size=%d part=%d", len(wav), session.PartSize)
	}
	parts := [][]byte{wav[:session.PartSize], wav[session.PartSize:]}
	for index, part := range parts {
		digest := sha256.Sum256(part)
		request(t, client, http.MethodPut,
			fmt.Sprintf("%s/api/v1/uploads/%s/parts/%d", server.URL, session.ID, index+1),
			map[string]string{
				"Content-Type": "application/octet-stream", "Origin": publicOrigin,
				"X-Part-SHA256": hex.EncodeToString(digest[:]),
			}, bytes.NewReader(part), http.StatusCreated)
	}
	completeResponse := request(t, client, http.MethodPost,
		server.URL+"/api/v1/uploads/"+session.ID+"/complete",
		map[string]string{"Origin": publicOrigin}, nil, http.StatusOK)
	var completed upload.Session
	decode(t, completeResponse.body, &completed)
	if completed.State != upload.StateCompleted {
		t.Fatalf("completed upload state = %q", completed.State)
	}

	jobResponse := request(t, client, http.MethodPost,
		server.URL+"/api/v1/assets/"+createdAsset.ID+"/transcriptions",
		map[string]string{"Origin": publicOrigin, "Idempotency-Key": "e2e-transcription"},
		nil, http.StatusAccepted)
	var queuedJob job.Job
	decode(t, jobResponse.body, &queuedJob)
	if queuedJob.State != job.StateQueued {
		t.Fatalf("queued job state = %q", queuedJob.State)
	}
	processor := transcription.NewProcessor(
		jobRepository, assetRepository, asr.NewMockProvider(), localStorage,
		transcription.NewPostgresCommitter(pool), "e2e-worker",
	)
	processed, err := processor.RunOnce(ctx)
	if err != nil || !processed {
		t.Fatalf("worker RunOnce() = (%t, %v)", processed, err)
	}
	finishedJobResponse := request(t, client, http.MethodGet,
		server.URL+"/api/v1/transcription-jobs/"+queuedJob.ID, nil, nil, http.StatusOK)
	var finishedJob job.Job
	decode(t, finishedJobResponse.body, &finishedJob)
	if finishedJob.State != job.StateSucceeded || finishedJob.ResultRevisionID == nil {
		t.Fatalf("finished job = %+v", finishedJob)
	}

	transcriptsResponse := request(t, client, http.MethodGet,
		server.URL+"/api/v1/assets/"+createdAsset.ID+"/transcripts", nil, nil, http.StatusOK)
	var summaries struct {
		Items []transcript.Summary `json:"items"`
	}
	decode(t, transcriptsResponse.body, &summaries)
	if len(summaries.Items) != 1 || summaries.Items[0].LatestRevisionID != *finishedJob.ResultRevisionID {
		t.Fatalf("transcript summaries = %+v", summaries.Items)
	}
	revisionResponse := request(t, client, http.MethodGet,
		server.URL+"/api/v1/transcript-revisions/"+*finishedJob.ResultRevisionID,
		nil, nil, http.StatusOK)
	var revision transcript.Revision
	decode(t, revisionResponse.body, &revision)
	if revision.Kind != transcript.KindNormalized || revision.ParentRevisionID == "" ||
		revision.Text != "Welcome to VoiceAsset." || len(revision.Segments) != 2 {
		t.Fatalf("normalized transcript revision = %+v", revision)
	}

	audioResponse := request(t, client, http.MethodGet,
		server.URL+"/api/v1/assets/"+createdAsset.ID+"/audio",
		map[string]string{"Range": "bytes=44-47"}, nil, http.StatusPartialContent)
	if !bytes.Equal(audioResponse.body, wav[44:48]) || audioResponse.header.Get("Content-Range") != fmt.Sprintf("bytes 44-47/%d", len(wav)) {
		t.Fatalf("audio range = %q / %q", audioResponse.body, audioResponse.header.Get("Content-Range"))
	}
	headResponse := request(t, client, http.MethodHead,
		server.URL+"/api/v1/assets/"+createdAsset.ID+"/audio", nil, nil, http.StatusOK)
	if len(headResponse.body) != 0 || headResponse.header.Get("Content-Length") != fmt.Sprint(len(wav)) {
		t.Fatalf("audio HEAD = body:%d length:%q", len(headResponse.body), headResponse.header.Get("Content-Length"))
	}
}

type response struct {
	header http.Header
	body   []byte
}

func request(
	t *testing.T,
	client *http.Client,
	method,
	url string,
	headers map[string]string,
	body io.Reader,
	wantStatus int,
) response {
	t.Helper()
	req, err := http.NewRequestWithContext(context.Background(), method, url, body)
	if err != nil {
		t.Fatalf("create %s request: %v", method, err)
	}
	for name, value := range headers {
		req.Header.Set(name, value)
	}
	res, err := client.Do(req)
	if err != nil {
		t.Fatalf("perform %s request: %v", method, err)
	}
	defer res.Body.Close()
	content, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatalf("read %s response: %v", method, err)
	}
	if res.StatusCode != wantStatus {
		t.Fatalf("%s %s status = %d, want %d: %s", method, req.URL.Path, res.StatusCode, wantStatus, content)
	}
	return response{header: res.Header.Clone(), body: content}
}

func decode(t *testing.T, content []byte, destination any) {
	t.Helper()
	if err := json.Unmarshal(content, destination); err != nil {
		t.Fatalf("decode response: %v", err)
	}
}

func stringsReader(value string) io.Reader {
	return bytes.NewBufferString(value)
}

func validTwoPartWAV() []byte {
	totalSize := upload.DefaultPartSize + 1024
	dataSize := totalSize - 44
	result := make([]byte, totalSize)
	copy(result[0:4], "RIFF")
	binary.LittleEndian.PutUint32(result[4:8], uint32(totalSize-8))
	copy(result[8:12], "WAVE")
	copy(result[12:16], "fmt ")
	binary.LittleEndian.PutUint32(result[16:20], 16)
	binary.LittleEndian.PutUint16(result[20:22], 1)
	binary.LittleEndian.PutUint16(result[22:24], 1)
	binary.LittleEndian.PutUint32(result[24:28], 16_000)
	binary.LittleEndian.PutUint32(result[28:32], 32_000)
	binary.LittleEndian.PutUint16(result[32:34], 2)
	binary.LittleEndian.PutUint16(result[34:36], 16)
	copy(result[36:40], "data")
	binary.LittleEndian.PutUint32(result[40:44], uint32(dataSize))
	return result
}

func migratedE2EPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("TEST_DATABASE_URL is not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	t.Cleanup(cancel)
	admin, err := pgx.Connect(ctx, databaseURL)
	if err != nil {
		t.Fatalf("connect to PostgreSQL")
	}
	t.Cleanup(func() { _ = admin.Close(context.Background()) })
	schema := fmt.Sprintf("phase1_e2e_%d", time.Now().UnixNano())
	identifier := pgx.Identifier{schema}.Sanitize()
	if _, err := admin.Exec(ctx, "CREATE SCHEMA "+identifier); err != nil {
		t.Fatalf("create isolated schema")
	}
	t.Cleanup(func() {
		if _, err := admin.Exec(context.Background(), "DROP SCHEMA IF EXISTS "+identifier+" CASCADE"); err != nil {
			t.Errorf("drop isolated schema")
		}
	})
	config, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		t.Fatalf("parse database configuration")
	}
	config.ConnConfig.RuntimeParams["search_path"] = schema
	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		t.Fatalf("create pool")
	}
	t.Cleanup(pool.Close)
	connection, err := pool.Acquire(ctx)
	if err != nil {
		t.Fatalf("acquire migration connection")
	}
	files, err := migration.Load(filepath.Join("..", "..", "migrations"))
	if err != nil {
		connection.Release()
		t.Fatalf("load migrations: %v", err)
	}
	if _, err := migration.Apply(ctx, connection.Conn(), files); err != nil {
		connection.Release()
		t.Fatalf("apply migrations: %v", err)
	}
	connection.Release()
	return pool
}
