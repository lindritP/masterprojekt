// Typed client for the public API (SRS §5.3). Every call goes through
// identity-gateway with the user's access token.
import { config } from "./config";
import { userManager } from "./auth";

export type CaseType = "BUILDING_PERMIT" | "RESIDENCE_REGISTRATION" | "BENEFIT_CLAIM";
export type CaseState = "DRAFT" | "VALIDATING" | "NEEDS_INFO" | "UNDER_REVIEW" | "ACCEPTED" | "REJECTED";
export type DocumentStatus =
  | "PENDING_UPLOAD" | "UPLOADED" | "VALIDATING" | "VALID" | "INVALID" | "SUPERSEDED" | "DELETED";

export interface CaseSummary {
  id: string;
  reference: string;
  caseType: CaseType;
  state: CaseState;
  title: string;
  createdAt: string;
  updatedAt: string;
  documentCount: number;
}

export interface Validation {
  outcome: "PASS" | "FAIL";
  failureCodes: string[];
  checks: { detectedMime: string; sizeBytes: number; sha256: string; pageCount: number; contentDigest: string; thumbnailKey: string };
}

export interface CaseDocument {
  id: string;
  filename: string;
  declaredMime: string;
  detectedMime: string | null;
  sizeBytes: number | null;
  sha256: string | null;
  status: DocumentStatus;
  supersedesId: string | null;
  createdAt: string;
  uploadedAt: string | null;
  validatedAt: string | null;
  validation: Validation | null;
}

export interface CaseDetail extends CaseSummary {
  version: number;
  submittedAt: string | null;
  decidedAt: string | null;
  decidedBy: string | null;
  decisionReason: string | null;
  documents: CaseDocument[];
}

export interface AuditEntry {
  id: number;
  occurredAt: string;
  actorType: "CITIZEN" | "CASEWORKER" | "SYSTEM";
  actorId: string;
  eventType: string;
  correlationId: string;
  payload: Record<string, unknown>;
}

interface Page<T> {
  items: T[];
  nextCursor: string | null;
}

/** An RFC 9457 problem returned by the API (SRS §5.2). */
export class ApiError extends Error {
  constructor(public status: number, public code: string, detail: string) {
    super(detail);
  }
}

async function request<T>(method: string, path: string, body?: unknown, headers: Record<string, string> = {}): Promise<T> {
  const user = await userManager.getUser();
  const res = await fetch(config.apiBaseUrl + path, {
    method,
    headers: {
      ...(body !== undefined ? { "Content-Type": "application/json" } : {}),
      ...(user ? { Authorization: `Bearer ${user.access_token}` } : {}),
      ...headers,
    },
    body: body !== undefined ? JSON.stringify(body) : undefined,
  });
  if (!res.ok) {
    const problem = await res.json().catch(() => ({}));
    throw new ApiError(res.status, problem.code ?? "INTERNAL_ERROR", problem.detail ?? res.statusText);
  }
  return res.status === 204 ? (undefined as T) : res.json();
}

const ifMatch = (version: number) => ({ "If-Match": `"${version}"` });
const idempotent = () => ({ "Idempotency-Key": crypto.randomUUID() });

export const api = {
  listCases: (state?: CaseState) =>
    request<Page<CaseSummary>>("GET", `/cases?limit=100${state ? `&state=${state}` : ""}`),
  createCase: (caseType: CaseType, title: string) => request<CaseDetail>("POST", "/cases", { caseType, title }),
  getCase: (id: string) => request<CaseDetail>("GET", `/cases/${id}`),
  events: (id: string) => request<Page<AuditEntry>>("GET", `/cases/${id}/events?limit=100`),
  submit: (c: CaseDetail) =>
    request<{ state: CaseState }>("POST", `/cases/${c.id}/submit`, undefined, { ...ifMatch(c.version), ...idempotent() }),
  deleteDocument: (c: CaseDetail, documentId: string) =>
    request<void>("DELETE", `/cases/${c.id}/documents/${documentId}`, undefined, ifMatch(c.version)),
  downloadUrl: (caseId: string, documentId: string) =>
    request<{ downloadUrl: string }>("GET", `/cases/${caseId}/documents/${documentId}/download`),

  /** Registers a file, uploads it straight to object storage, then confirms it. */
  async uploadDocument(caseId: string, file: File): Promise<void> {
    const declaredMime = file.type || "application/pdf";
    const slot = await request<{
      documentId: string;
      uploadUrl: string;
      uploadMethod: "PUT" | "POST";
      requiredHeaders: Record<string, string>;
      formFields: Record<string, string>;
    }>("POST", `/cases/${caseId}/documents`, { filename: file.name, declaredMime, sizeBytes: file.size });

    let res: Response;
    if (slot.uploadMethod === "POST") {
      const form = new FormData();
      Object.entries(slot.formFields).forEach(([k, v]) => form.append(k, v));
      form.append("file", file);
      res = await fetch(slot.uploadUrl, { method: "POST", body: form });
    } else {
      res = await fetch(slot.uploadUrl, { method: "PUT", body: file, headers: { ...slot.requiredHeaders, "Content-Type": declaredMime } });
    }
    if (!res.ok) throw new ApiError(res.status, "UPLOAD_REJECTED", "Der Speicher hat den Upload abgelehnt.");

    await request("POST", `/cases/${caseId}/documents/${slot.documentId}/complete`, {}, idempotent());
  },

  worker: {
    listCases: (state?: CaseState) =>
      request<Page<CaseSummary>>("GET", `/worker/cases?limit=100${state ? `&state=${state}` : ""}`),
    getCase: (id: string) => request<CaseDetail>("GET", `/worker/cases/${id}`),
    decide: (c: CaseDetail, decision: "ACCEPTED" | "REJECTED", reason: string) =>
      request<{ state: CaseState }>("POST", `/worker/cases/${c.id}/decision`, { decision, reason }, {
        ...ifMatch(c.version),
        ...idempotent(),
      }),
  },
};
