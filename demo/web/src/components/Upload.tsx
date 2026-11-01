import { useRef, useState } from "react";

/** A drop zone that hands the chosen PDFs to onFiles. */
export function Upload({ onFiles, busy }: { onFiles: (files: File[]) => void; busy: boolean }) {
  const input = useRef<HTMLInputElement>(null);
  const [over, setOver] = useState(false);
  return (
    <div
      className={`dropzone ${over ? "over" : ""} ${busy ? "busy" : ""}`}
      onClick={() => !busy && input.current?.click()}
      onDragOver={(e) => {
        e.preventDefault();
        setOver(true);
      }}
      onDragLeave={() => setOver(false)}
      onDrop={(e) => {
        e.preventDefault();
        setOver(false);
        if (!busy) onFiles([...e.dataTransfer.files]);
      }}
    >
      <input
        ref={input}
        type="file"
        accept="application/pdf"
        multiple
        hidden
        onChange={(e) => {
          onFiles([...(e.target.files ?? [])]);
          e.target.value = "";
        }}
      />
      {busy ? "Wird hochgeladen …" : <>PDF hierher ziehen oder <u>auswählen</u></>}
      <div className="muted small">Nur PDF, höchstens 20 MiB und 50 Seiten. Gleicher Dateiname ersetzt ein fehlerhaftes Dokument.</div>
    </div>
  );
}
