import { ComponentProps, ReactNode } from "react";

// Shared table styling for the progress and history views.

export function Table({
  headers,
  className = "",
  children,
}: {
  headers: string[];
  className?: string;
  children: ReactNode;
}) {
  return (
    <table
      className={`mt-3 text-sm text-left rtl:text-right text-gray-500 dark:text-gray-400 justify-self-center ${className}`}
    >
      <thead>
        <tr className="text-xs text-gray-700 uppercase bg-gray-50 dark:bg-gray-700 dark:text-gray-400">
          {headers.map((header) => (
            <th key={header} scope="col" className="px-6 py-3">
              {header}
            </th>
          ))}
        </tr>
      </thead>
      <tbody>{children}</tbody>
    </table>
  );
}

// Tr and Td append a caller's className to their own, as Input does.

export function Tr({ className = "", ...props }: ComponentProps<"tr">) {
  return (
    <tr
      className={`odd:bg-white odd:dark:bg-gray-900 even:bg-gray-50 even:dark:bg-gray-800 border-b dark:border-gray-700 border-gray-200 ${className}`}
      {...props}
    />
  );
}

export function Td({ className = "", ...props }: ComponentProps<"td">) {
  return <td className={`px-6 py-4 ${className}`} {...props} />;
}
