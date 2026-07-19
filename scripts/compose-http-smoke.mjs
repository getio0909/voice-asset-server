import { createHash } from "node:crypto";

const baseURL = process.env.COMPOSE_SMOKE_BASE_URL ?? "http://127.0.0.1:8080";
const origin = process.env.COMPOSE_SMOKE_ORIGIN ?? baseURL;
const email = process.env.COMPOSE_SMOKE_EMAIL;
const password = process.env.COMPOSE_SMOKE_PASSWORD;

if (!email || !password) {
  throw new Error("COMPOSE_SMOKE_EMAIL and COMPOSE_SMOKE_PASSWORD are required");
}

let sessionCookie = "";

function parseCookie(response) {
  const values = typeof response.headers.getSetCookie === "function"
    ? response.headers.getSetCookie()
    : [response.headers.get("set-cookie") ?? ""];
  const value = values.find(Boolean);
  if (!value) return "";
  return value.split(";", 1)[0];
}

async function request(path, options = {}, expectedStatus = 200) {
  const headers = new Headers(options.headers ?? {});
  headers.set("Origin", origin);
  if (sessionCookie) headers.set("Cookie", sessionCookie);
  const response = await fetch(`${baseURL}${path}`, { ...options, headers });
  const contentType = response.headers.get("content-type") ?? "";
  const body = contentType.includes("json")
    ? await response.json()
    : new Uint8Array(await response.arrayBuffer());
  if (response.status !== expectedStatus) {
    const detail = typeof body === "object" ? JSON.stringify(body) : `${body.length} bytes`;
    throw new Error(`${options.method ?? "GET"} ${path} returned ${response.status}, expected ${expectedStatus}: ${detail}`);
  }
  return { response, body };
}

function jsonBody(value) {
  return JSON.stringify(value);
}

function sha256(value) {
  return createHash("sha256").update(value).digest("hex");
}

function makeWAV() {
  const totalSize = 5 * 1024 * 1024 + 1024;
  const wav = Buffer.alloc(totalSize, 1);
  wav.write("RIFF", 0, "ascii");
  wav.writeUInt32LE(totalSize - 8, 4);
  wav.write("WAVEfmt ", 8, "ascii");
  wav.writeUInt32LE(16, 16);
  wav.writeUInt16LE(1, 20);
  wav.writeUInt16LE(1, 22);
  wav.writeUInt32LE(16_000, 24);
  wav.writeUInt32LE(32_000, 28);
  wav.writeUInt16LE(2, 32);
  wav.writeUInt16LE(16, 34);
  wav.write("data", 36, "ascii");
  wav.writeUInt32LE(totalSize - 44, 40);
  return wav;
}

const login = await request(
  "/api/v1/auth/sessions",
  {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: jsonBody({ email, password }),
  },
  201,
);
if (JSON.stringify(login.body).includes("access_token")) {
  throw new Error("web login response exposed an access token");
}
sessionCookie = parseCookie(login.response);
if (!sessionCookie) throw new Error("web login did not set a session cookie");

const assetResponse = await request(
  "/api/v1/assets",
  {
    method: "POST",
    headers: { "Content-Type": "application/json", "Idempotency-Key": "compose-smoke-asset" },
    body: jsonBody({ title: "Compose HTTP Smoke Recording", language: "en-US" }),
  },
  201,
);
const assetID = assetResponse.body.id;
if (typeof assetID !== "string" || !assetID) throw new Error("asset response did not include an id");
const initialAssetETag = assetResponse.response.headers.get("etag");
if (!initialAssetETag) throw new Error("asset response did not include an ETag");

const wav = makeWAV();
const uploadResponse = await request(
  "/api/v1/uploads",
  {
    method: "POST",
    headers: { "Content-Type": "application/json", "Idempotency-Key": "compose-smoke-upload" },
    body: jsonBody({
      asset_id: assetID,
      filename: "compose-smoke.wav",
      mime_type: "audio/wav",
      size_bytes: wav.length,
      sha256: sha256(wav),
    }),
  },
  201,
);
const session = uploadResponse.body;
if (session.part_size !== 5 * 1024 * 1024 || wav.length <= session.part_size) {
  throw new Error(`upload fixture did not exercise two parts: size=${wav.length} part=${session.part_size}`);
}
for (const [index, part] of [wav.subarray(0, session.part_size), wav.subarray(session.part_size)].entries()) {
  await request(
    `/api/v1/uploads/${session.id}/parts/${index + 1}`,
    {
      method: "PUT",
      headers: { "Content-Type": "application/octet-stream", "X-Part-SHA256": sha256(part) },
      body: part,
    },
    201,
  );
}
const completed = await request(`/api/v1/uploads/${session.id}/complete`, { method: "POST" }, 200);
if (completed.body.state !== "completed") throw new Error(`upload state = ${completed.body.state}`);

const jobResponse = await request(`/api/v1/assets/${assetID}/transcriptions`, {
  method: "POST",
  headers: { "Idempotency-Key": "compose-smoke-transcription" },
}, 202);
const jobID = jobResponse.body.id;
if (typeof jobID !== "string" || !jobID) throw new Error("transcription response did not include a job id");

let finishedJob;
for (let attempt = 0; attempt < 45; attempt += 1) {
  const current = await request(`/api/v1/transcription-jobs/${jobID}`);
  finishedJob = current.body;
  if (finishedJob.state === "succeeded") break;
  if (finishedJob.state === "failed") throw new Error(`transcription job failed: ${finishedJob.error_code ?? "unknown"}`);
  await new Promise((resolve) => setTimeout(resolve, 1000));
}
if (finishedJob?.state !== "succeeded" || typeof finishedJob.result_revision_id !== "string") {
  throw new Error(`transcription job did not finish: ${JSON.stringify(finishedJob)}`);
}

const summaries = await request(`/api/v1/assets/${assetID}/transcripts`);
if (summaries.body.items?.length !== 1 || summaries.body.items[0].latest_revision_id !== finishedJob.result_revision_id) {
  throw new Error(`unexpected transcript summaries: ${JSON.stringify(summaries.body)}`);
}
const revision = await request(`/api/v1/transcript-revisions/${finishedJob.result_revision_id}`);
if (revision.body.kind !== "normalized" || revision.body.text !== "Welcome to VoiceAsset." || revision.body.segments?.length !== 2) {
  throw new Error(`unexpected normalized transcript: ${JSON.stringify(revision.body)}`);
}

const ranged = await request(`/api/v1/assets/${assetID}/audio`, { headers: { Range: "bytes=44-47" } }, 206);
if (Buffer.from(ranged.body).compare(wav.subarray(44, 48)) !== 0 || ranged.response.headers.get("content-range") !== `bytes 44-47/${wav.length}`) {
  throw new Error("audio range response did not match the uploaded bytes");
}
const head = await request(`/api/v1/assets/${assetID}/audio`, { method: "HEAD" });
if (head.body.length !== 0 || head.response.headers.get("content-length") !== String(wav.length)) {
  throw new Error("audio HEAD response did not match the uploaded size");
}

const metadata = await request(
  `/api/v1/assets/${assetID}/metadata`,
  {
    method: "PUT",
    headers: { "Content-Type": "application/json", "If-Match": initialAssetETag },
    body: jsonBody({ title: "Compose Lifecycle Recording", language: "en-US", collection_id: null }),
  },
  200,
);
const metadataETag = metadata.response.headers.get("etag");
if (!metadataETag || metadata.body.title !== "Compose Lifecycle Recording") {
  throw new Error(`metadata update did not return the new title and ETag: ${JSON.stringify(metadata.body)}`);
}

const exportResponse = await request(
  `/api/v1/transcript-revisions/${finishedJob.result_revision_id}/exports`,
  {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: jsonBody({ format: "markdown" }),
  },
  201,
);
const exportID = exportResponse.body.id;
const exportURL = exportResponse.body.download_url;
if (typeof exportID !== "string" || typeof exportURL !== "string" || !exportURL.startsWith("/api/v1/transcript-exports/")) {
  throw new Error(`transcript export response was incomplete: ${JSON.stringify(exportResponse.body)}`);
}
const downloadedExport = await request(exportURL);
const exportText = Buffer.from(downloadedExport.body).toString("utf8");
if (!exportText.includes("Welcome to VoiceAsset.")) {
  throw new Error("transcript export did not contain the normalized transcript");
}

const trashed = await request(
  `/api/v1/assets/${assetID}`,
  { method: "DELETE", headers: { "If-Match": metadataETag } },
  200,
);
const trashedETag = trashed.response.headers.get("etag");
if (!trashedETag || trashed.body.status !== "trashed") {
  throw new Error(`asset trash response was incomplete: ${JSON.stringify(trashed.body)}`);
}
await request(`/api/v1/assets/${assetID}`, {}, 404);
const trashedList = await request("/api/v1/assets?status=trashed");
if (!trashedList.body.items?.some((asset) => asset.id === assetID && asset.status === "trashed")) {
  throw new Error("trashed asset was not returned by the explicit trash filter");
}

const restored = await request(
  `/api/v1/assets/${assetID}/restore`,
  { method: "POST", headers: { "If-Match": trashedETag } },
  200,
);
if (restored.body.status !== "ready" || restored.body.title !== "Compose Lifecycle Recording") {
  throw new Error(`asset restore did not preserve lifecycle state: ${JSON.stringify(restored.body)}`);
}
await request(`/api/v1/assets/${assetID}`);

console.log("Compose HTTP smoke passed: login, multipart upload, Mock ASR, transcript, range playback, export, metadata, trash, and restore.");
