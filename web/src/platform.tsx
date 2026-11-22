import type { Platform, SourceState } from "./types";

export const PLATFORM: Record<Platform, { label: string; color: string; glyph: string }> = {
  wechat: { label: "微信", color: "#07C160", glyph: "微" },
  feishu: { label: "飞书", color: "#3370FF", glyph: "飞" },
  qq: { label: "QQ", color: "#12B7F5", glyph: "Q" },
  discord: { label: "Discord", color: "#5865F2", glyph: "D" },
  mail: { label: "邮件", color: "#E8890C", glyph: "@" },
};

export const STATE_LABEL: Record<SourceState, string> = {
  ok: "正常",
  syncing: "同步中",
  connecting: "连接中",
  error: "异常",
  disabled: "未启用",
};

export function PlatformMark({ platform, size = 16 }: { platform: Platform; size?: number }) {
  const p = PLATFORM[platform] ?? PLATFORM.mail;
  return (
    <span
      className="pmark"
      style={{ background: p.color, width: size, height: size, fontSize: Math.round(size * 0.62) }}
      aria-label={p.label}
      title={p.label}
    >
      {p.glyph}
    </span>
  );
}

const AVATAR_TINTS = ["#E9DCF3", "#F6E1D3", "#D8E6F7", "#F3D9E2", "#EFE3C2", "#D9EADF", "#E6E0D6"];

export function tintFor(key: string): string {
  let h = 0;
  for (let i = 0; i < key.length; i++) h = (h * 31 + key.charCodeAt(i)) | 0;
  return AVATAR_TINTS[Math.abs(h) % AVATAR_TINTS.length];
}
