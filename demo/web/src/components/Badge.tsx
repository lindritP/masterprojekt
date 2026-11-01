import type { CaseState, DocumentStatus } from "../api";
import { documentStatusLabel, stateLabel } from "../labels";

export function StateBadge({ state }: { state: CaseState }) {
  return <span className={`badge state-${state.toLowerCase()}`}>{stateLabel[state]}</span>;
}

export function DocumentBadge({ status }: { status: DocumentStatus }) {
  return <span className={`badge doc-${status.toLowerCase()}`}>{documentStatusLabel[status]}</span>;
}
