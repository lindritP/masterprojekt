import { useCallback, useEffect, useState } from "react";
import { api, type AuditEntry, type CaseDetail, type CaseDocument } from "../api";
import { DocumentBadge, StateBadge } from "../components/Badge";
import { ErrorBox } from "../components/ErrorBox";
import { Steps } from "../components/Steps";
import { Timeline } from "../components/Timeline";
import { Upload } from "../components/Upload";
import { caseTypeLabel, failureLabel, formatBytes, formatDate, stateHint, workerHint } from "../labels";

interface Props {
  id: string;
  mode: "citizen" | "worker";
}

/** One case, as its citizen or as a caseworker sees it. */
export function CaseView({ id, mode }: Props) {
  const [c, setCase] = useState<CaseDetail | null>(null);
  const [events, setEvents] = useState<AuditEntry[]>([]);
  const [error, setError] = useState<unknown>(null);
  const [busy, setBusy] = useState(false);
  const [showAll, setShowAll] = useState(false);

  const load = useCallback(async () => {
    try {
      const fresh = mode === "worker" ? await api.worker.getCase(id) : await api.getCase(id);
      setCase(fresh);
      setEvents((await api.events(id)).items);
    } catch (err) {
      setError(err);
    }
  }, [id, mode]);

  useEffect(() => {
    load();
  }, [load]);

  // While the worker is busy, keep the view fresh.
  const pending =
    c !== null &&
    (c.state === "VALIDATING" || c.documents.some((d) => d.status === "VALIDATING" || (d.status === "UPLOADED" && !d.validation)));
  useEffect(() => {
    if (!pending) return;
    const t = setInterval(load, 1500);
    return () => clearInterval(t);
  }, [pending, load]);

  async function run(action: () => Promise<unknown>) {
    setBusy(true);
    setError(null);
    try {
      await action();
    } catch (err) {
      setError(err);
    } finally {
      setBusy(false);
      await load();
    }
  }

  if (!c) {
    return error ? <ErrorBox error={error} /> : <p className="muted">Wird geladen …</p>;
  }

  const editable = mode === "citizen" && (c.state === "DRAFT" || c.state === "NEEDS_INFO");
  const submittable = editable && c.documents.some((d) => d.status === "UPLOADED");
  const visibleDocs = c.documents.filter((d) => showAll || (d.status !== "SUPERSEDED" && d.status !== "DELETED"));
  const hidden = c.documents.length - c.documents.filter((d) => d.status !== "SUPERSEDED" && d.status !== "DELETED").length;

  return (
    <div className="case">
      <a className="back" href={mode === "worker" ? "#/worker" : "#/cases"}>
        ← zurück
      </a>

      <div className="card case-head">
        <div>
          <h2>{c.title}</h2>
          <div className="muted">
            <code>{c.reference}</code> · {caseTypeLabel[c.caseType]} · angelegt {formatDate(c.createdAt)}
            {c.submittedAt && <> · eingereicht {formatDate(c.submittedAt)}</>}
          </div>
        </div>
        <StateBadge state={c.state} />
      </div>

      <Steps state={c.state} />
      <p className="hint">{(mode === "worker" ? workerHint : stateHint)[c.state]}</p>

      {(c.state === "ACCEPTED" || c.state === "REJECTED") && (
        <div className={`card decision ${c.state === "ACCEPTED" ? "ok" : "no"}`}>
          <h3>Bescheid: {c.state === "ACCEPTED" ? "Antrag angenommen" : "Antrag abgelehnt"}</h3>
          {c.decisionReason && <p>„{c.decisionReason}“</p>}
          <div className="muted small">entschieden am {formatDate(c.decidedAt)}</div>
        </div>
      )}

      <ErrorBox error={error} onClose={() => setError(null)} />

      <section className="card">
        <div className="section-head">
          <h3>Dokumente</h3>
          {hidden > 0 && (
            <button className="link" onClick={() => setShowAll(!showAll)}>
              {showAll ? "Ersetzte ausblenden" : `${hidden} ersetzte/entfernte anzeigen`}
            </button>
          )}
        </div>
        {visibleDocs.length === 0 ? (
          <p className="muted">Noch keine Dokumente.</p>
        ) : (
          <table className="table">
            <thead>
              <tr>
                <th>Datei</th>
                <th>Status</th>
                <th>Größe</th>
                <th>Prüfergebnis</th>
                <th />
              </tr>
            </thead>
            <tbody>
              {visibleDocs.map((d) => (
                <DocumentRow
                  key={d.id}
                  doc={d}
                  mode={mode}
                  canDelete={editable && d.status !== "DELETED" && d.status !== "SUPERSEDED"}
                  onDownload={() =>
                    run(async () => {
                      const { downloadUrl } = await api.downloadUrl(c.id, d.id);
                      window.open(downloadUrl, "_blank", "noopener");
                    })
                  }
                  onDelete={() => run(() => api.deleteDocument(c, d.id))}
                />
              ))}
            </tbody>
          </table>
        )}

        {editable && (
          <Upload
            busy={busy}
            onFiles={(files) =>
              run(async () => {
                for (const file of files) await api.uploadDocument(c.id, file);
              })
            }
          />
        )}
      </section>

      {editable && (
        <div className="actions">
          <button className="primary large" disabled={!submittable || busy} onClick={() => run(() => api.submit(c))}>
            {c.state === "NEEDS_INFO" ? "Erneut einreichen" : "Antrag einreichen"}
          </button>
          {!submittable && <span className="muted small">Erst nach einem erfolgreichen Upload möglich.</span>}
        </div>
      )}

      {mode === "worker" && c.state === "UNDER_REVIEW" && <DecisionForm busy={busy} onDecide={(d, r) => run(() => api.worker.decide(c, d, r))} />}

      <section className="card">
        <h3>Vorgangshistorie</h3>
        <Timeline entries={events} />
      </section>
    </div>
  );
}

function DocumentRow(props: {
  doc: CaseDocument;
  mode: "citizen" | "worker";
  canDelete: boolean;
  onDownload: () => void;
  onDelete: () => void;
}) {
  const { doc: d } = props;
  const v = d.validation;
  return (
    <tr className={d.status === "SUPERSEDED" || d.status === "DELETED" ? "faded" : ""}>
      <td>
        <div>{d.filename}</div>
        <div className="muted small">{formatDate(d.uploadedAt ?? d.createdAt)}</div>
      </td>
      <td>
        <DocumentBadge status={d.status} />
      </td>
      <td>{formatBytes(d.sizeBytes)}</td>
      <td>
        {!v ? (
          d.status === "UPLOADED" || d.status === "VALIDATING" ? <span className="muted">wird geprüft …</span> : <span className="muted">–</span>
        ) : v.outcome === "PASS" ? (
          <span className="ok">
            ✓ {v.checks.pageCount} Seite{v.checks.pageCount === 1 ? "" : "n"}
            {props.mode === "worker" && (
              <div className="muted small" title="Inhalts-Prüfsumme (FR-410)">
                Prüfsumme <code>{v.checks.contentDigest.slice(0, 12)}</code>
              </div>
            )}
          </span>
        ) : (
          <span className="no">✗ {v.failureCodes.map((f) => failureLabel[f] ?? f).join(", ")}</span>
        )}
      </td>
      <td className="row-actions">
        {d.status !== "PENDING_UPLOAD" && d.status !== "DELETED" && (
          <button className="link" onClick={props.onDownload}>
            Herunterladen
          </button>
        )}
        {props.canDelete && (
          <button className="link danger" onClick={props.onDelete}>
            Entfernen
          </button>
        )}
      </td>
    </tr>
  );
}

function DecisionForm({ busy, onDecide }: { busy: boolean; onDecide: (d: "ACCEPTED" | "REJECTED", reason: string) => void }) {
  const [reason, setReason] = useState("");
  return (
    <section className="card decide">
      <h3>Bescheid erlassen</h3>
      <label>
        Begründung <span className="muted small">(bei Ablehnung Pflicht)</span>
        <textarea value={reason} onChange={(e) => setReason(e.target.value)} rows={3} maxLength={2000} />
      </label>
      <div className="actions">
        <button className="primary" disabled={busy} onClick={() => onDecide("ACCEPTED", reason)}>
          Annehmen
        </button>
        <button className="danger-button" disabled={busy || !reason.trim()} onClick={() => onDecide("REJECTED", reason)}>
          Ablehnen
        </button>
      </div>
    </section>
  );
}

