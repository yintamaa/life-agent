import { useState } from "react";
import { PlatformMark, tintFor } from "../platform";
import type { Platform } from "../types";

export function Avatar({ title, src, platform, size = 36, badge = true }: { title: string; src?: string; platform: Platform; size?: number; badge?: boolean }) {
  const [broken, setBroken] = useState(false);
  const initial = Array.from(title.trim())[0] ?? "?";
  return (
    <span className="avatar" style={{ width: size, height: size }}>
      {src && !broken ? (
        <img src={src} alt="" onError={() => setBroken(true)} referrerPolicy="no-referrer" />
      ) : (
        <span className="avatar-fallback" style={{ background: tintFor(title), fontSize: Math.round(size * 0.42) }}>{initial}</span>
      )}
      {badge && <span className="avatar-badge"><PlatformMark platform={platform} size={Math.round(size * 0.4)} /></span>}
    </span>
  );
}
