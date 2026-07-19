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

const glossary = await request(
  "/api/v1/glossary-sets",
  {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: jsonBody({
      display_name: "Compose correction terms",
      scope_type: "workspace",
      entries: [{
        canonical_form: "Greetings",
        aliases: ["Welcome"],
        language: "en-US",
        context_terms: [],
        forbidden_contexts: [],
        regex: false,
        case_sensitive: false,
        priority: 100,
        description: "Compose Mock LLM correction fixture",
      }],
    }),
  },
  201,
);
if (glossary.body.current_version !== 1 || typeof glossary.body.id !== "string") {
  throw new Error(`glossary creation did not publish version one: ${JSON.stringify(glossary.body)}`);
}

const llmProfile = await request(
  "/api/v1/llm-profiles",
  {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: jsonBody({
      provider_id: "mock_llm",
      display_name: "Compose Mock correction",
      state: "enabled",
      priority: 1,
      config: {
        model: "deterministic_glossary_v1",
        timeout: "30s",
        concurrency: 32,
        temperature: 0,
        context_limit: 64000,
        structured_output: true,
        prompt_template: "correction.v1",
        default_glossary_id: glossary.body.id,
        auto_approval_policy: "never",
      },
    }),
  },
  201,
);
if (llmProfile.body.provider_id !== "mock_llm" || llmProfile.body.secret_configured !== false) {
  throw new Error(`Mock LLM profile exposed unexpected configuration: ${JSON.stringify(llmProfile.body)}`);
}

const correctionResponse = await request(
  `/api/v1/transcript-revisions/${finishedJob.result_revision_id}/corrections`,
  { method: "POST", headers: { "Idempotency-Key": "compose-smoke-correction" } },
  202,
);
const correctionJobID = correctionResponse.body.id;
if (typeof correctionJobID !== "string" || !correctionJobID) {
  throw new Error("correction response did not include a job id");
}

let finishedCorrection;
for (let attempt = 0; attempt < 45; attempt += 1) {
  const current = await request(`/api/v1/transcription-jobs/${correctionJobID}`);
  finishedCorrection = current.body;
  if (finishedCorrection.state === "succeeded") break;
  if (finishedCorrection.state === "failed") {
    throw new Error(`correction job failed: ${finishedCorrection.error_code ?? "unknown"}`);
  }
  await new Promise((resolve) => setTimeout(resolve, 1000));
}
if (finishedCorrection?.state !== "succeeded" || typeof finishedCorrection.result_revision_id !== "string") {
  throw new Error(`correction job did not finish: ${JSON.stringify(finishedCorrection)}`);
}

const correctedRevision = await request(`/api/v1/transcript-revisions/${finishedCorrection.result_revision_id}`);
if (
  correctedRevision.body.kind !== "llm_corrected" ||
  correctedRevision.body.parent_revision_id !== finishedJob.result_revision_id ||
  correctedRevision.body.text !== "Greetings to VoiceAsset." ||
  correctedRevision.body.diff?.changes?.length !== 1
) {
  throw new Error(`unexpected corrected transcript: ${JSON.stringify(correctedRevision.body)}`);
}
await request(
  `/api/v1/transcript-revisions/${finishedCorrection.result_revision_id}/reviews`,
  {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: jsonBody({ action: "accept_all" }),
  },
  201,
);
const approval = await request(
  `/api/v1/transcript-revisions/${finishedCorrection.result_revision_id}/approve`,
  {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: jsonBody({}),
  },
  201,
);
if (
  approval.body.human_revision?.kind !== "human_edited" ||
  approval.body.approved_revision?.kind !== "approved" ||
  approval.body.approved_revision?.text !== "Greetings to VoiceAsset."
) {
  throw new Error(`unexpected approval result: ${JSON.stringify(approval.body)}`);
}
await request(
  `/api/v1/transcript-revisions/${finishedCorrection.result_revision_id}/approve`,
  { method: "POST", headers: { "Content-Type": "application/json" }, body: jsonBody({}) },
  409,
);

const currentAsset = await request(`/api/v1/assets/${assetID}`);
const currentAssetETag = currentAsset.response.headers.get("etag");
if (!currentAssetETag) throw new Error("asset response did not include the current ETag");

const metadata = await request(
  `/api/v1/assets/${assetID}/metadata`,
  {
    method: "PUT",
    headers: { "Content-Type": "application/json", "If-Match": currentAssetETag },
    body: jsonBody({ title: "Compose Lifecycle Recording", language: "en-US", collection_id: null }),
  },
  200,
);
const metadataETag = metadata.response.headers.get("etag");
if (!metadataETag || metadata.body.title !== "Compose Lifecycle Recording") {
  throw new Error(`metadata update did not return the new title and ETag: ${JSON.stringify(metadata.body)}`);
}

const exportResponse = await request(
  `/api/v1/transcript-revisions/${approval.body.approved_revision.id}/exports`,
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
if (!exportText.includes("Greetings to VoiceAsset.")) {
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

console.log("Compose HTTP smoke passed: login, multipart upload, Mock ASR/LLM, transcript review/approval, range playback, export, metadata, trash, and restore.");
