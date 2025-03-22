import { useEffect, useState } from "react";

function useSse(
  url: string,
  messageKey: string,
  endMessageKey: string,
  setData: (arg0: any) => void
) {
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    const eventSource = new EventSource(url);

    // Handle incoming data
    eventSource.addEventListener(messageKey, (e) => {
      if (e.data) {
        setData(JSON.parse(e.data));
        setError("");
      }
    });

    eventSource.addEventListener(endMessageKey, () => {
      console.log("Close Connection to server events");
      eventSource.close();
      setError("");
    });

    eventSource.onopen = () => {
      console.log("Listening for server events.");
    };

    // Handle errors
    eventSource.onerror = () => {
      setError("Connection lost to server events..");
    };

    // Cleanup when component unmounts
    return () => eventSource.close();
  }, [url, messageKey, endMessageKey]);

  return { error };
}

export default useSse;
