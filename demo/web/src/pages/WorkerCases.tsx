import { useEffect, useState } from "react";
import { api, type CaseState, type CaseSummary } from "../api";
import { StateBadge } from "../components/Badge";
import { ErrorBox } from "../components/ErrorBox";
import { caseTypeLabel, formatDate, stateLabel } from "../labels";

const filters: (CaseState | undefined)[] = ["UNDER_REVIEW", "NEEDS_INFO", "VALIDATING", "ACCEPTED", "REJECTED", undefined];

export function WorkerCases() {
  const [filter, setFilter] = useState<CaseState | undefined>("UNDER_REVIEW");
  const [cases, setCases] = useState<CaseSummary[] | null>(null);
  const [error, setError] = useState<unknown>(null);

  useEffect(() => {
    setCases(null);
    api.worker.listCases(filter).then((p) => setCases(p.items), setError);
  }, [filter]);

  return (
    <section>
      <h2>Sachbearbeitung</h2>
      <div className="tabs">
        {filters.map((f) => (
          <button key={f ?? "all"} className={f === filter ? "tab active" : "tab"} onClick={() => setFilter(f)}>
            {f ? stateLabel[f] : "Alle"}
          </button>
        ))}
      </div>
      <ErrorBox error={error} onClose={() => setError(null)} />
      {cases === null ? (
        <p className="muted">Wird geladen …</p>
      ) : cases.length === 0 ? (
        <div className="card empty">Keine Vorgänge in diesem Status.</div>
      ) : (
        <table className="table card">
          <thead>
            <tr>
              <th>Aktenzeichen</th>
              <th>Bezeichnung</th>
              <th>Art</th>
              <th>Dokumente</th>
              <th>Zuletzt geändert</th>
              <th>Status</th>
            </tr>
          </thead>
          <tbody>
            {cases.map((c) => (
              <tr key={c.id} onClick={() => (window.location.hash = `/worker/${c.id}`)} className="clickable">
                <td>
                  <code>{c.reference}</code>
                </td>
                <td>{c.title}</td>
                <td>{caseTypeLabel[c.caseType]}</td>
                <td>{c.documentCount}</td>
                <td>{formatDate(c.updatedAt)}</td>
                <td>
                  <StateBadge state={c.state} />
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      )}
    </section>
  );
}
