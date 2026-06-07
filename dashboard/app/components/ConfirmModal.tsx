import * as Dialog from "@radix-ui/react-dialog";
import type { ReactNode } from "react";

interface ConfirmModalProps {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  title: string;
  description?: string;
  details?: { label: string; value: string }[];
  confirmLabel?: string;
  cancelLabel?: string;
  variant?: "danger" | "default";
  children?: ReactNode;
  onConfirm: () => void;
}

export function ConfirmModal({
  open,
  onOpenChange,
  title,
  description,
  details,
  confirmLabel = "Confirm",
  cancelLabel = "Cancel",
  variant = "default",
  children,
  onConfirm,
}: ConfirmModalProps) {
  return (
    <Dialog.Root open={open} onOpenChange={onOpenChange}>
      <Dialog.Portal>
        <Dialog.Overlay className="fixed inset-0 bg-black/40 z-40" />
        <Dialog.Content className="fixed top-1/2 left-1/2 -translate-x-1/2 -translate-y-1/2 bg-white dark:bg-gray-800 rounded-lg shadow-xl p-6 w-full max-w-md z-50">
          <Dialog.Title className="text-lg font-semibold text-gray-900 dark:text-gray-100">
            {title}
          </Dialog.Title>

          {description && (
            <Dialog.Description className="mt-2 text-sm text-gray-600 dark:text-gray-400">
              {description}
            </Dialog.Description>
          )}

          {details && details.length > 0 && (
            <div className="mt-3 space-y-1">
              {details.map((d) => (
                <div key={d.label} className="text-sm">
                  <span className="text-gray-500 dark:text-gray-400">
                    {d.label}:{" "}
                  </span>
                  <span className="text-gray-900 dark:text-gray-100 font-medium">
                    {d.value}
                  </span>
                </div>
              ))}
            </div>
          )}

          {children && <div className="mt-3">{children}</div>}

          <div className="mt-6 flex justify-end gap-3">
            <Dialog.Close asChild>
              <button
                type="button"
                className="px-4 py-2 text-sm font-medium text-gray-700 bg-gray-100 hover:bg-gray-200 dark:text-gray-200 dark:bg-gray-700 dark:hover:bg-gray-600 rounded-md"
              >
                {cancelLabel}
              </button>
            </Dialog.Close>
            <button
              type="button"
              onClick={() => {
                onConfirm();
                onOpenChange(false);
              }}
              className={`px-4 py-2 text-sm font-medium text-white rounded-md ${
                variant === "danger"
                  ? "bg-red-600 hover:bg-red-700"
                  : "bg-blue-600 hover:bg-blue-700"
              }`}
            >
              {confirmLabel}
            </button>
          </div>
        </Dialog.Content>
      </Dialog.Portal>
    </Dialog.Root>
  );
}
