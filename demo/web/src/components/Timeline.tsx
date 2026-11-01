import type { AuditEntry } from "../api";
import { actorLabel, eventLabel, formatDate, stateLabel } from "../labels";
import type { CaseState } from "../api";

function describe(e: AuditEntry): string {
  const p = e.payload;
  if (e.eventType === "case.state_changed") {
    const to = stateLabel[p.to as CaseState] ?? String(p.to);
    const reason = p.reason ? ` – „${p.reason}“` : "";
    return `${stateLabel[p.from as CaseState] ?? p.from} → ${to}${reason}`;
  }
  if (e.eventType === "document.attached" || e.eventType === "document.uploaded" || e.eventType === "document.deleted") {
    return String(p.filename ?? "");
  }
  if (e.eventType === "document.validated") {
    return p.outcome === "PASS" ? "bestanden" : `nicht bestanden (${(p.failureCodes as string[] | undefined)?.join(", ")})`;
  }
  if (e.eventType === "case.created") return String(p.reference ?? "");
  return "";
}

export function Timeline({ entries }: { entries: AuditEntry[] }) {
  if (entries.length === 0) return <p className="muted">Noch keine Einträge.</p>;
  return (
    <ul className="timeline">
      {[...entries].reverse().map((e) => (
        <li key={e.id}>
          <div className="when">{formatDate(e.occurredAt)}</div>
          <div>
            <strong>{eventLabel[e.eventType] ?? e.eventType}</strong> <span className="muted">{describe(e)}</span>
            <div className="who">
              {actorLabel[e.actorType]} · <code title="Correlation-ID">{e.correlationId.slice(0, 13)}</code>
            </div>
          </div>
        </li>
      ))}
    </ul>
  );
}
