import {
  createContext,
  useCallback,
  useContext,
  useEffect,
  useMemo,
  useState,
  type ReactNode,
} from "react";

// ---- Namespace selection (global, persisted) ----

interface NamespaceContextValue {
  namespace: string; // "" = all namespaces
  setNamespace: (ns: string) => void;
}

const NamespaceContext = createContext<NamespaceContextValue>({
  namespace: "",
  setNamespace: () => {},
});

export function NamespaceProvider({ children }: { children: ReactNode }) {
  const [namespace, setNamespaceState] = useState(
    () => localStorage.getItem("namespace") ?? "",
  );
  const setNamespace = useCallback((ns: string) => {
    localStorage.setItem("namespace", ns);
    setNamespaceState(ns);
  }, []);
  const value = useMemo(() => ({ namespace, setNamespace }), [namespace, setNamespace]);
  return <NamespaceContext.Provider value={value}>{children}</NamespaceContext.Provider>;
}

export const useNamespace = () => useContext(NamespaceContext);

// ---- Theme (light/dark, persisted, defaults to system) ----

export function useTheme() {
  const [dark, setDark] = useState(() => {
    const stored = localStorage.getItem("theme");
    if (stored) return stored === "dark";
    return window.matchMedia("(prefers-color-scheme: dark)").matches;
  });

  useEffect(() => {
    document.documentElement.classList.toggle("dark", dark);
    localStorage.setItem("theme", dark ? "dark" : "light");
  }, [dark]);

  return { dark, toggle: () => setDark((d) => !d) };
}
