import { ComponentProps } from "react";

// Shared styling for text-like inputs.
export default function Input({
  className = "",
  ...props
}: ComponentProps<"input">) {
  return (
    <input
      className={`bg-gray-50 border border-gray-300 text-gray-900 text-sm rounded-lg focus:ring-blue-500 focus:border-blue-500 p-2.5 dark:bg-gray-700 dark:border-gray-600 dark:placeholder-gray-400 dark:text-white dark:focus:ring-blue-500 dark:focus:border-blue-500 ${className}`}
      {...props}
    />
  );
}
