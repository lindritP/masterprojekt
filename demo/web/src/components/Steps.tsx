import type { CaseState } from "../api";

// The happy path of SRS §3.2, with NEEDS_INFO shown as a detour at step 2.
const steps: { label: string; states: CaseState[] }[] = [
  { label: "Antrag erstellen", states: ["DRAFT"] },
  { label: "Automatische Prüfung", states: ["VALIDATING", "NEEDS_INFO"] },
  { label: "Sachbearbeitung", states: ["UNDER_REVIEW"] },
  { label: "Bescheid", states: ["ACCEPTED", "REJECTED"] },
];

export function Steps({ state }: { state: CaseState }) {
  // An accepted case has completed every step.
  const current = state === "ACCEPTED" ? steps.length : steps.findIndex((s) => s.states.includes(state));
  return (
    <ol className="steps">
      {steps.map((s, i) => {
        const status = i < current ? "done" : i === current ? (state === "NEEDS_INFO" || state === "REJECTED" ? "warn" : "current") : "todo";
        return (
          <li key={s.label} className={`step ${status}`}>
            <span className="dot">{i < current ? "✓" : i + 1}</span>
            <span>{i === 1 && state === "NEEDS_INFO" ? "Nachforderung" : s.label}</span>
          </li>
        );
      })}
    </ol>
  );
}
