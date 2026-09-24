import { useEffect, useRef, useState } from "react";

function useSse<T>(
  url: string,
  messageKey: string,
  endMessageKey: string,
  onData: (data: T) => void
) {
  const [error, setError] = useState<string | null>(null);

  // Keep the latest callback without reconnecting when its identity changes.
  const onDataRef = useRef(onData);
  useEffect(() => {
    onDataRef.current = onData;
  });

  useEffect(() => {
    const eventSource = new EventSource(url);

    // Handle incoming data
    eventSource.addEventListener(messageKey, (e) => {
      if (e.data) {
        onDataRef.current(JSON.parse(e.data) as T);
        setError(null);
      }
    });

    eventSource.addEventListener(endMessageKey, () => {
      eventSource.close();
      setError(null);
    });

    eventSource.onopen = () => {
      setError(null);
    };

    // Handle errors. While the browser is reconnecting on its own the state
    // is CONNECTING; only a CLOSED source has given up.
    eventSource.onerror = () => {
      if (eventSource.readyState === EventSource.CLOSED) {
        setError("Connection lost to server events..");
      }
    };

    // Cleanup when component unmounts
    return () => eventSource.close();
  }, [url, messageKey, endMessageKey]);

  return { error };
}

export default useSse;
