// Client-side persistence for AI Assistant conversations. Stored in the
// browser's localStorage (not on the server): it survives reloads and gives a
// browsable history, while keeping each browser's prompts private — the
// server has no auth, so server-side chat storage would share everyone's
// conversations. History is per-browser by design.

export interface ChatStep {
  kind: "text" | "tool" | "error";
  content: string;
}

export interface ChatEntry {
  role: "user" | "assistant";
  steps: ChatStep[];
  done: boolean;
}

export interface Conversation {
  id: string;
  title: string;
  updatedAt: number; // epoch ms
  chat: ChatEntry[];
}

const KEY = "kentinel.conversations";
const MAX_CONVERSATIONS = 40;

export function newConversationId(): string {
  return Date.now().toString(36) + Math.random().toString(36).slice(2, 8);
}

// titleFor derives a short title from the first user message.
export function titleFor(chat: ChatEntry[]): string {
  const firstUser = chat.find((e) => e.role === "user");
  const text = firstUser?.steps.map((s) => s.content).join(" ").trim() ?? "";
  if (!text) return "New conversation";
  return text.length > 48 ? text.slice(0, 48) + "…" : text;
}

export function loadConversations(): Conversation[] {
  try {
    const raw = localStorage.getItem(KEY);
    if (!raw) return [];
    const parsed = JSON.parse(raw);
    if (!Array.isArray(parsed)) return [];
    // Basic shape check; ignore anything malformed rather than crashing.
    return parsed.filter(
      (c): c is Conversation =>
        c && typeof c.id === "string" && Array.isArray(c.chat),
    );
  } catch {
    return [];
  }
}

// upsertConversation saves (or replaces) a conversation and returns the new
// list, newest-first, capped. Empty conversations are not persisted.
export function upsertConversation(conv: Conversation): Conversation[] {
  const list = loadConversations().filter((c) => c.id !== conv.id);
  if (conv.chat.length > 0) {
    list.unshift(conv);
  }
  list.sort((a, b) => b.updatedAt - a.updatedAt);
  const capped = list.slice(0, MAX_CONVERSATIONS);
  persist(capped);
  return capped;
}

export function deleteConversation(id: string): Conversation[] {
  const list = loadConversations().filter((c) => c.id !== id);
  persist(list);
  return list;
}

function persist(list: Conversation[]) {
  try {
    localStorage.setItem(KEY, JSON.stringify(list));
  } catch {
    // Quota exceeded or storage disabled — history just won't persist.
  }
}
