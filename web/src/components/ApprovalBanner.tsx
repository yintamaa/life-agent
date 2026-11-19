import { ChevronRight, ShieldAlert } from "lucide-react";

/** Compact, tap-friendly banner: "N 个操作等待审批" → 待办 / 待审批. */
export function ApprovalBanner({ count, onOpen }: { count: number; onOpen: () => void }) {
  if (count <= 0) return null;
  return (
    <button className="approval-banner" onClick={onOpen}>
      <ShieldAlert size={15} strokeWidth={2} />
      <span><b>{count}</b> 个操作等待审批</span>
      <ChevronRight size={14} className="approval-banner-go" />
    </button>
  );
}
