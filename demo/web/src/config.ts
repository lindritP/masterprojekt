// Runtime configuration from public/config.js (overwritten by the container).
export interface Config {
  apiBaseUrl: string;
  issuer: string;
  clientId: string;
}

declare global {
  interface Window {
    CCSRA_CONFIG?: Partial<Config>;
  }
}

export const config: Config = {
  apiBaseUrl: "http://localhost:8080/api/v1",
  issuer: "http://localhost:8180/realms/ccsra",
  clientId: "ccsra-web",
  ...window.CCSRA_CONFIG,
};
