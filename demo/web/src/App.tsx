import { useEffect, useState } from "react";
import { restoreSession, toSession, userManager, type Session } from "./auth";
import { CaseView } from "./pages/CaseView";
import { CitizenCases } from "./pages/CitizenCases";
import { Landing } from "./pages/Landing";
import { WorkerCases } from "./pages/WorkerCases";
import { navigate, useRoute } from "./router";

export function App() {
  const [session, setSession] = useState<Session | null | undefined>(undefined);
  const [loginError, setLoginError] = useState<unknown>(null);
  const route = useRoute();

  useEffect(() => {
    restoreSession().then(setSession, (err) => {
      setLoginError(err);
      setSession(null);
      window.history.replaceState({}, "", window.location.pathname);
    });
    const loaded = (u: Parameters<typeof toSession>[0]) => setSession(toSession(u));
    const gone = () => setSession(null);
    userManager.events.addUserLoaded(loaded);
    userManager.events.addSilentRenewError(gone);
    userManager.events.addUserSignedOut(gone);
    return () => {
      userManager.events.removeUserLoaded(loaded);
      userManager.events.removeSilentRenewError(gone);
      userManager.events.removeUserSignedOut(gone);
    };
  }, []);

  const isCaseworker = session?.roles.includes("caseworker") ?? false;
  const isCitizen = session?.roles.includes("citizen") ?? false;

  useEffect(() => {
    if (session && route.length === 0) navigate(isCaseworker ? "/worker" : "/cases");
  }, [session, route, isCaseworker]);

  if (session === undefined) return <div className="center muted">Wird geladen …</div>;

  return (
    <>
      <div className="demo-banner">Demo-Oberfläche zur Vorführung – nicht Teil der Messung (ADR 0014)</div>
      <header className="topbar">
        <a className="brand" href="#/">
          <span className="logo">C</span> Bürgerportal <span className="muted">CCSRA</span>
        </a>
        {session && (
          <nav>
            {isCitizen && (
              <a className={route[0] === "cases" ? "active" : ""} href="#/cases">
                Meine Vorgänge
              </a>
            )}
            {isCaseworker && (
              <a className={route[0] === "worker" ? "active" : ""} href="#/worker">
                Sachbearbeitung
              </a>
            )}
          </nav>
        )}
        {session && (
          <div className="user">
            <span>{session.name}</span>
            <span className="chip">{isCaseworker ? "Sachbearbeiter:in" : "Antragsteller:in"}</span>
            <button className="link" onClick={() => userManager.signoutRedirect()}>
              Abmelden
            </button>
          </div>
        )}
      </header>
      <main className="container">
        {!session ? (
          <Landing error={loginError} />
        ) : route[0] === "worker" && isCaseworker ? (
          route[1] ? <CaseView id={route[1]} mode="worker" /> : <WorkerCases />
        ) : route[0] === "cases" && route[1] ? (
          <CaseView id={route[1]} mode="citizen" />
        ) : isCitizen ? (
          <CitizenCases />
        ) : (
          <WorkerCases />
        )}
      </main>
    </>
  );
}
