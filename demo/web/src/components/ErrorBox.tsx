import { ApiError } from "../api";
import { errorLabel } from "../labels";

export function message(err: unknown): string {
  if (err instanceof ApiError) return errorLabel[err.code] ?? err.message;
  if (err instanceof TypeError) return "Der Server ist nicht erreichbar. Läuft `make dev`?";
  return String(err);
}

export function ErrorBox({ error, onClose }: { error: unknown; onClose?: () => void }) {
  if (!error) return null;
  return (
    <div className="alert" role="alert">
      <span>{message(error)}</span>
      {onClose && (
        <button className="link" onClick={onClose} aria-label="Schließen">
          ✕
        </button>
      )}
    </div>
  );
}
