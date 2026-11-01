// German wording of the UI, following the ubiquitous language of SRS §3.4.
import type { CaseState, CaseType, DocumentStatus } from "./api";

export const caseTypeLabel: Record<CaseType, string> = {
  BUILDING_PERMIT: "Baugenehmigung",
  RESIDENCE_REGISTRATION: "Wohnsitzmeldung",
  BENEFIT_CLAIM: "Leistungsantrag",
};

export const stateLabel: Record<CaseState, string> = {
  DRAFT: "Entwurf",
  VALIDATING: "In Prüfung",
  NEEDS_INFO: "Nachforderung",
  UNDER_REVIEW: "In Bearbeitung",
  ACCEPTED: "Angenommen",
  REJECTED: "Abgelehnt",
};

export const stateHint: Record<CaseState, string> = {
  DRAFT: "Laden Sie Ihre Nachweise hoch und reichen Sie den Antrag ein.",
  VALIDATING: "Ihre Dokumente werden automatisch geprüft.",
  NEEDS_INFO: "Mindestens ein Dokument ist fehlerhaft. Bitte laden Sie einen Ersatz hoch und reichen Sie erneut ein.",
  UNDER_REVIEW: "Alle Dokumente sind in Ordnung. Ihr Antrag liegt bei der Sachbearbeitung.",
  ACCEPTED: "Ihr Antrag wurde angenommen.",
  REJECTED: "Ihr Antrag wurde abgelehnt.",
};

export const workerHint: Record<CaseState, string> = {
  DRAFT: "Der Antrag ist noch nicht eingereicht.",
  VALIDATING: "Die Dokumente werden gerade automatisch geprüft.",
  NEEDS_INFO: "Die antragstellende Person muss fehlerhafte Dokumente ersetzen.",
  UNDER_REVIEW: "Alle Dokumente sind gültig. Bitte prüfen und den Bescheid erlassen.",
  ACCEPTED: "Der Antrag wurde angenommen.",
  REJECTED: "Der Antrag wurde abgelehnt.",
};

export const documentStatusLabel: Record<DocumentStatus, string> = {
  PENDING_UPLOAD: "Upload ausstehend",
  UPLOADED: "Hochgeladen",
  VALIDATING: "Wird geprüft",
  VALID: "Gültig",
  INVALID: "Ungültig",
  SUPERSEDED: "Ersetzt",
  DELETED: "Entfernt",
};

export const failureLabel: Record<string, string> = {
  FILE_TOO_LARGE: "Datei zu groß",
  UNSUPPORTED_MEDIA_TYPE: "Dateityp nicht erlaubt",
  MIME_MISMATCH: "Inhalt passt nicht zum Dateityp",
  CORRUPT_DOCUMENT: "PDF ist beschädigt",
  ENCRYPTED_DOCUMENT: "PDF ist verschlüsselt",
  PAGE_COUNT_OUT_OF_RANGE: "Zu viele oder keine Seiten",
  RENDER_FAILED: "PDF lässt sich nicht darstellen",
};

export const errorLabel: Record<string, string> = {
  UNAUTHENTICATED: "Ihre Anmeldung ist abgelaufen. Bitte melden Sie sich erneut an.",
  FORBIDDEN_ROLE: "Dafür fehlt Ihnen die Berechtigung.",
  FORBIDDEN_NOT_OWNER: "Dieser Vorgang gehört einer anderen Person.",
  CASE_NOT_FOUND: "Der Vorgang wurde nicht gefunden.",
  DOCUMENT_NOT_FOUND: "Das Dokument wurde nicht gefunden.",
  INVALID_STATE_TRANSITION: "Das ist im aktuellen Status nicht möglich.",
  CONCURRENT_MODIFICATION: "Der Vorgang wurde inzwischen geändert. Die Ansicht wurde aktualisiert, bitte erneut versuchen.",
  NO_DOCUMENTS_ATTACHED: "Bitte laden Sie zuerst mindestens ein Dokument hoch.",
  DOCUMENTS_IMMUTABLE: "Dokumente können in diesem Status nicht mehr geändert werden.",
  UNSUPPORTED_MEDIA_TYPE: "Es werden nur PDF-Dateien angenommen.",
  PAYLOAD_TOO_LARGE: "Die Datei ist zu groß (höchstens 20 MiB).",
  DOCUMENT_LIMIT_EXCEEDED: "Ein Vorgang kann höchstens 10 Dokumente haben.",
  DECISION_REASON_REQUIRED: "Eine Ablehnung braucht eine Begründung.",
  UPLOAD_NOT_COMPLETED: "Der Upload ist nicht vollständig angekommen.",
  UPLOAD_REJECTED: "Der Speicher hat den Upload abgelehnt.",
  VALIDATION_FAILED: "Die Eingabe ist ungültig.",
  RATE_LIMITED: "Zu viele Anfragen. Bitte kurz warten.",
  UPSTREAM_UNAVAILABLE: "Ein Dienst ist gerade nicht erreichbar.",
};

export const eventLabel: Record<string, string> = {
  "case.created": "Vorgang angelegt",
  "document.attached": "Dokument angemeldet",
  "document.uploaded": "Dokument hochgeladen",
  "document.validated": "Dokument automatisch geprüft",
  "document.deleted": "Dokument entfernt",
  "case.state_changed": "Status geändert",
};

export const actorLabel = { CITIZEN: "Antragsteller:in", CASEWORKER: "Sachbearbeiter:in", SYSTEM: "System" } as const;

export function formatDate(iso: string | null): string {
  if (!iso) return "–";
  return new Date(iso).toLocaleString("de-AT", { dateStyle: "medium", timeStyle: "short" });
}

export function formatBytes(n: number | null): string {
  if (n == null) return "–";
  if (n < 1024) return `${n} B`;
  if (n < 1024 * 1024) return `${(n / 1024).toFixed(0)} KB`;
  return `${(n / 1024 / 1024).toFixed(1)} MB`;
}
