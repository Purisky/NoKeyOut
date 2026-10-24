/**
 * omp / Pi provider extension — registers a "nokeyout" provider whose model list
 * is discovered live from the gateway's `GET /v1/models`.
 *
 * Installed as a package it loads automatically on every omp startup, so the
 * list is pulled at startup with no `-e` flag and no environment variables.
 *
 * Config file (optional):  ~/.omp/agent/nokeyout-provider.json   (mode 0600)
 *   { "baseUrl": "http://192.0.2.10:3425/v1",
 *     "gatewayKey": "sk-…",
 *     "providerId": "nokeyout" }
 *
 * Resolution order per key: environment variable > config file > default.
 *   NOKEYOUT_BASE_URL / NOKEYOUT_GATEWAY_KEY / NOKEYOUT_PROVIDER_ID
 *   NOKEYOUT_CONFIG_PATH  overrides the config file location
 *
 * In-session:  /nokeyout-refresh  re-fetches the list and reports the count.
 *
 * Notes:
 *  - The gateway speaks OpenAI Chat Completions, so we reuse the built-in
 *    `openai-completions` API implementation: no protocol/streaming code here.
 *  - The initial fetch happens in the (async) factory with a timeout, so models
 *    are present at startup without letting a hung gateway stall it;
 *    `refreshModels` keeps the list live afterwards.
 */

import { readFileSync } from "node:fs";
import { homedir } from "node:os";
import { join } from "node:path";

/** Model definition in the shape `registerProvider` expects for a chat model. */
type ChatModel = {
  type: "chat";
  id: string;
  name: string;
  api: string;
  baseUrl: string;
  input: string[];
  cost: { input: number; output: number; cacheRead: number; cacheWrite: number };
  contextWindow: number;
  maxTokens: number;
  reasoning: boolean;
};

/** Structural view of the parts of Pi's ExtensionAPI this extension uses. */
type ExtensionAPI = {
  registerProvider(
    name: string,
    config: {
      baseUrl: string;
      api: string;
      apiKey: string;
      models: ChatModel[];
      refreshModels: (ctx: { signal?: AbortSignal }) => Promise<ChatModel[]>;
      headers?: Record<string, string>;
      compat?: Record<string, unknown>;
    },
  ): void;
  registerCommand(
    name: string,
    command: {
      description: string;
      handler: (arg: string, ctx: { ui: { notify: (message: string, level: string) => void } }) => Promise<void>;
    },
  ): void;
};

/** Read `~/.omp/agent/nokeyout-provider.json`; missing or malformed means defaults. */
function readFileConfig(): { baseUrl?: string; gatewayKey?: string; providerId?: string } {
  const path = process.env.NOKEYOUT_CONFIG_PATH ?? join(homedir(), ".omp", "agent", "nokeyout-provider.json");
  let raw: unknown;
  try {
    raw = JSON.parse(readFileSync(path, "utf8"));
  } catch {
    return {};
  }
  if (typeof raw !== "object" || raw === null) return {};
  const config: { baseUrl?: string; gatewayKey?: string; providerId?: string } = {};
  if ("baseUrl" in raw && typeof raw.baseUrl === "string") config.baseUrl = raw.baseUrl;
  if ("gatewayKey" in raw && typeof raw.gatewayKey === "string") config.gatewayKey = raw.gatewayKey;
  if ("providerId" in raw && typeof raw.providerId === "string") config.providerId = raw.providerId;
  return config;
}

const FILE_CONFIG = readFileConfig();

/** Provider id shown to the agent (`provider/model-id`). Override to run several gateways. */
const PROVIDER_ID = process.env.NOKEYOUT_PROVIDER_ID ?? FILE_CONFIG.providerId ?? "nokeyout";
const BASE = (process.env.NOKEYOUT_BASE_URL ?? FILE_CONFIG.baseUrl ?? "http://192.0.2.10:3425/v1").replace(/\/+$/, "");
const GATEWAY_KEY = process.env.NOKEYOUT_GATEWAY_KEY ?? FILE_CONFIG.gatewayKey ?? "";

/**
 * 扩展注册时携带的两个 header：
 *  - headers["User-Agent"]: magpie 按 UA 识别 agent 并归类用量；不带会被记为未知客户端。
 *  - compat.promptCacheSessionHeader: 让 omp 发 x-session-affinity，magpie 的
 *    `stays=auto` 缓存感知路由才能把同一会话留在同一账号上（缓存才值钱）。
 */
const USER_AGENT = process.env.NOKEYOUT_USER_AGENT ?? "omp";
const SESSION_HEADER = process.env.NOKEYOUT_SESSION_HEADER ?? "x-session-affinity";

/** Map one `GET /v1/models` entry onto a Pi chat-model definition. */
function toModelDef(raw: unknown): ChatModel | undefined {
  if (typeof raw !== "object" || raw === null || !("id" in raw)) return undefined;
  const id = raw.id; // `in` narrowing yields unknown; checked next line.
  if (typeof id !== "string" || id.length === 0) return undefined;
  const name = "name" in raw && typeof raw.name === "string" ? raw.name : id;
  const contextWindow =
    "context_window" in raw && typeof raw.context_window === "number" ? raw.context_window : 200000;
  const maxTokens =
    "max_output_tokens" in raw && typeof raw.max_output_tokens === "number" ? raw.max_output_tokens : 32000;
  const reasoning =
    ("reasoning" in raw && raw.reasoning === true) ||
    ("supported_reasoning_levels" in raw &&
      Array.isArray(raw.supported_reasoning_levels) &&
      raw.supported_reasoning_levels.length > 0);
  return {
    type: "chat",
    id,
    name,
    api: "openai-completions",
    baseUrl: BASE,
    input: ["text"],
    cost: { input: 0, output: 0, cacheRead: 0, cacheWrite: 0 },
    contextWindow,
    maxTokens,
    reasoning,
  };
}

/** Live model list for the nokeyout provider. Throws on transport/HTTP failure. */
async function fetchModels(signal?: AbortSignal): Promise<ChatModel[]> {
  const res = await fetch(`${BASE}/models`, {
    headers: GATEWAY_KEY ? { Authorization: `Bearer ${GATEWAY_KEY}` } : {},
    signal: signal ?? AbortSignal.timeout(8000),
  });
  if (!res.ok) throw new Error(`nokeyout /v1/models -> HTTP ${res.status}`);
  const body: unknown = await res.json();
  const data =
    typeof body === "object" && body !== null && "data" in body && Array.isArray(body.data) ? body.data : [];
  return data.map(toModelDef).filter((m): m is ChatModel => m !== undefined);
}

export default async function (pi: ExtensionAPI): Promise<void> {
  let models: ChatModel[] = [];
  try {
    models = await fetchModels();
  } catch (err: unknown) {
    // Don't break startup on a gateway hiccup; refreshModels will retry.
    const message = err instanceof Error ? err.message : String(err);
    console.error(`[nokeyout-provider] initial discovery failed: ${message}`);
  }

  pi.registerProvider(PROVIDER_ID, {
    baseUrl: BASE,
    api: "openai-completions",
    apiKey: GATEWAY_KEY,
    models,
    refreshModels: async (ctx: { signal?: AbortSignal }) => fetchModels(ctx.signal),
    headers: { "User-Agent": USER_AGENT },
    compat: { promptCacheSessionHeader: SESSION_HEADER },
  });

  pi.registerCommand("nokeyout-refresh", {
    description: `Re-fetch the ${PROVIDER_ID} gateway model list`,
    handler: async (_arg, ctx) => {
      try {
        const list = await fetchModels();
        ctx.ui.notify(`${PROVIDER_ID}: ${list.length} models from ${BASE}`, "info");
      } catch (err: unknown) {
        const message = err instanceof Error ? err.message : String(err);
        ctx.ui.notify(`${PROVIDER_ID}: refresh failed — ${message}`, "error");
      }
    },
  });
}
