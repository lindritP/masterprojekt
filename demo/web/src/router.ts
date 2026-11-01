// A minimal hash router: #/cases, #/cases/<id>, #/worker, #/worker/<id>.
import { useEffect, useState } from "react";

export function useRoute(): string[] {
  const read = () => window.location.hash.replace(/^#\/?/, "").split("/").filter(Boolean);
  const [route, setRoute] = useState(read);
  useEffect(() => {
    const onChange = () => setRoute(read());
    window.addEventListener("hashchange", onChange);
    return () => window.removeEventListener("hashchange", onChange);
  }, []);
  return route;
}

export const navigate = (path: string) => {
  window.location.hash = path;
};
