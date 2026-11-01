import { useEffect, useState } from "react";
import { api, type CaseSummary, type CaseType } from "../api";
import { StateBadge } from "../components/Badge";
import { ErrorBox } from "../components/ErrorBox";
import { caseTypeLabel, formatDate } from "../labels";
import { navigate } from "../router";

export function CitizenCases() {
  const [cases, setCases] = useState<CaseSummary[] | null>(null);
  const [error, setError] = useState<unknown>(null);
  const [caseType, setCaseType] = useState<CaseType>("BUILDING_PERMIT");
  const [title, setTitle] = useState("");
  const [busy, setBusy] = useState(false);

  useEffect(() => {
    api.listCases().then((p) => setCases(p.items), setError);
  }, []);

  async function create(e: React.FormEvent) {
    e.preventDefault();
    setBusy(true);
    try {
      const c = await api.createCase(caseType, title.trim());
      navigate(`/cases/${c.id}`);
    } catch (err) {
      setError(err);
    } finally {
      setBusy(false);
    }
  }

  return (
    <div className="grid two">
      <section>
        <h2>Meine Vorgänge</h2>
        <ErrorBox error={error} onClose={() => setError(null)} />
        {cases === null ? (
          <p className="muted">Wird geladen …</p>
        ) : cases.length === 0 ? (
          <div className="card empty">Noch keine Anträge. Legen Sie rechts Ihren ersten an.</div>
        ) : (
          <div className="list">
            {cases.map((c) => (
              <a key={c.id} className="card row-link" href={`#/cases/${c.id}`}>
                <div>
                  <div className="title">{c.title}</div>
                  <div className="muted small">
                    {c.reference} · {caseTypeLabel[c.caseType]} · {c.documentCount} Dokument
                    {c.documentCount === 1 ? "" : "e"} · {formatDate(c.updatedAt)}
                  </div>
                </div>
                <StateBadge state={c.state} />
              </a>
            ))}
          </div>
        )}
      </section>
      <aside>
        <form className="card form" onSubmit={create}>
          <h3>Neuer Antrag</h3>
          <label>
            Art des Antrags
            <select value={caseType} onChange={(e) => setCaseType(e.target.value as CaseType)}>
              {Object.entries(caseTypeLabel).map(([value, label]) => (
                <option key={value} value={value}>
                  {label}
                </option>
              ))}
            </select>
          </label>
          <label>
            Bezeichnung
            <input
              value={title}
              onChange={(e) => setTitle(e.target.value)}
              placeholder="z. B. Zubau Carport"
              maxLength={200}
              required
            />
          </label>
          <button className="primary" disabled={busy || !title.trim()}>
            Antrag anlegen
          </button>
        </form>
      </aside>
    </div>
  );
}
