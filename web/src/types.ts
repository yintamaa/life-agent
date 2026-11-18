export type Platform = "feishu" | "wechat" | "qq" | "mail";
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

export type Counts = Record<View, number>;

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
