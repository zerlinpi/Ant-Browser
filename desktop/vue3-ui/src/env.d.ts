/// <reference types="vite/client" />

interface Window {
  runtime?: Record<string, unknown>;
  go?: {
    main?: {
      App?: Record<string, (...args: unknown[]) => Promise<unknown>>;
    };
  };
}
