export type Platform = "feishu" | "wechat" | "qq" | "discord" | "mail";
export type SourceState = "ok" | "syncing" | "connecting" | "error" | "disabled";
export type View = "needs" | "all" | "unread" | "mention" | "p2p" | "group" | "alert";

export interface Source {
  id: string;
  platform: Platform;
  name: string;
  state: SourceState;
  last_sync_at: string | null;
  error: string;
}

export type Counts = Record<View, number> & { tasks_active?: number; approvals?: number };

export interface Conversation {
  id: string;
  source: string;
  platform: Platform;
  kind: "p2p" | "group" | "mail";
  title: string;
  avatar: string;
  last_message_at: string;
  last_preview: string;
  last_sender: string;
  unread: number;
  mentions: number;
  needs_attention: boolean;
  is_alert: boolean;
  can_send: boolean;
}

export type MessageType =
  | "text" | "image" | "file" | "card" | "voice" | "video" | "link" | "system" | "mail" | "other";

export interface Message {
  id: string;
  conversation_id: string;
  sender_id: string;
  sender_name: string;
  is_self: boolean;
  type: MessageType;
  text: string;
  subject: string;
  sent_at: string;
  mentions_me: boolean;
}

export interface SearchHit extends Message {
  conversation_title: string;
  platform: Platform;
}

// ---------- agent orchestration ----------

export type RuntimeName = "claude-code" | "codex";
export type TaskStatus = "queued" | "running" | "waiting_approval" | "idle" | "failed" | "cancelled" | "done";

export interface NodeInfo {
  name: string;
  runtimes: Array<{ name: RuntimeName; version: string; error?: string }>;
  workspaces: Array<{ name: string; path: string; worktree: boolean }>;
}

export interface NodeStatus {
  name: string;
  online: boolean;
  last_seen: string;
  info?: NodeInfo;
}

/** Named agent preset from config.yaml (where / which CLI / standing instructions). */
export interface AgentPreset {
  name: string;
  title: string;
  node: string;
  workspace: string;
  runtime: RuntimeName;
  model?: string;
  instructions?: string;
  available: boolean;
  reason?: string;
}

export interface Task {
  id: string;
  title: string;
  /** preset name, "" = custom */
  agent?: string;
  instruction: string;
  status: TaskStatus;
  node: string;
  workspace: string;
  runtime: RuntimeName;
  model: string;
  conversation_id: string;
  session_id: string;
  workdir: string;
  result: string;
  error: string;
  cost_usd: number;
  created_at: string;
  updated_at: string;
  pending_approvals: number;
}

export type TaskEventType =
  | "user" | "session" | "state" | "text" | "thinking" | "tool" | "tool_result"
  | "approval" | "approval_decided" | "log" | "error" | "result";

export interface TaskEventPayload {
  text?: string;
  tool?: string;
  input?: string;
  session_id?: string;
  cost_usd?: number;
  is_error?: boolean;
  // approval
  id?: string;
  task_id?: string;
  reason?: string;
  expires_at?: string;
  // approval_decided
  status?: "allowed" | "denied";
  by?: string;
}

export interface TaskEvent {
  seq: number;
  type: TaskEventType;
  payload: TaskEventPayload;
  at: string;
}

export type ApprovalStatus = "pending" | "allowed" | "denied" | "expired";

export interface Approval {
  id: string;
  task_id: string;
  task_title?: string;
  tool: string;
  input: string;
  reason: string;
  status: ApprovalStatus;
  decided_by: string;
  created_at: string;
  expires_at: string;
}

export interface CreateTaskReq {
  title?: string;
  instruction: string;
  conversation_id?: string;
  context_messages?: number;
  /** preset name; when set the server fills node/workspace/runtime/model */
  agent?: string;
  node?: string;
  workspace?: string;
  runtime?: RuntimeName;
  model?: string;
}

export type TaskFilter = "active" | "approvals" | "done" | "all";
