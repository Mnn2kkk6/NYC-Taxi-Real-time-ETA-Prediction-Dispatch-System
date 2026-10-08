import { createContext, useContext } from "react";

export const LiveEventsContext = createContext({ events: [], connected: false });

export function useLiveEventsContext() {
  return useContext(LiveEventsContext);
}
