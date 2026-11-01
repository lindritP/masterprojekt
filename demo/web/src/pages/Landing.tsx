import { userManager } from "../auth";

export function Landing({ error }: { error?: unknown }) {
  return (
    <div className="landing">
      {error != null && (
        <div className="alert" role="alert">
          Die Anmeldung ist fehlgeschlagen: {String((error as Error).message ?? error)}
        </div>
      )}
      <div className="hero card">
        <h1>Bürgerportal</h1>
        <p className="lead">
          Anträge digital einreichen, Nachweise hochladen und den Stand jederzeit verfolgen – und auf der anderen Seite
          als Sachbearbeiter:in prüfen und entscheiden.
        </p>
        <button className="primary large" onClick={() => userManager.signinRedirect()}>
          Anmelden
        </button>
        <div className="accounts">
          <div>
            <strong>Testkonten</strong> (lokaler Keycloak):
          </div>
          <ul>
            <li>
              <code>citizen-alice</code>, <code>citizen-bob</code> – Antragsteller:innen
            </li>
            <li>
              <code>caseworker-carol</code> – Sachbearbeiterin
            </li>
          </ul>
          <div className="muted small">Passwörter: deploy/local/keycloak/ccsra-realm.json</div>
        </div>
      </div>
      <div className="explain">
        <div className="card">
          <h3>Was passiert dahinter?</h3>
          <p>
            Vier Dienste in Containern: <em>identity-gateway</em> prüft das Token, <em>case-service</em> hält den
            Vorgang in PostgreSQL, <em>intake-service</em> stellt signierte Upload-URLs für den S3-Speicher aus, und{" "}
            <em>validation-worker</em> prüft jedes PDF asynchron über RabbitMQ.
          </p>
        </div>
        <div className="card">
          <h3>Wozu diese Oberfläche?</h3>
          <p>
            Sie dient nur der Vorführung. Gemessen wird die Anwendung über ihre API (SRS §2.2); diese Oberfläche ist
            nicht Teil der Messung und wird auf kein Cloud-Ziel deployt (ADR 0014).
          </p>
        </div>
      </div>
    </div>
  );
}
