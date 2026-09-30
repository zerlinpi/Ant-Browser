import { reactive } from "vue";
import { describeError } from "@/api/client";

export interface ConfirmOptions {
  title: string;
  message: string;
  confirmText?: string;
  danger?: boolean;
}

/**
 * Backing state for <ConfirmDialog>. The action runs inside the dialog so the
 * confirm button shows progress and failures stay visible next to the choice.
 */
export const useConfirm = () => {
  const state = reactive({ open: false, title: "", message: "", confirmText: "确认", danger: false, busy: false, error: "" });
  let action: (() => Promise<void>) | null = null;

  const ask = (options: ConfirmOptions, run: () => Promise<void>) => {
    Object.assign(state, {
      open: true,
      title: options.title,
      message: options.message,
      confirmText: options.confirmText ?? "确认",
      danger: options.danger ?? false,
      busy: false,
      error: "",
    });
    action = run;
  };

  const confirm = async () => {
    if (!action || state.busy) return;
    state.busy = true;
    state.error = "";
    try {
      await action();
      state.open = false;
      action = null;
    } catch (error) {
      state.error = describeError(error, "操作失败，请稍后重试");
    } finally {
      state.busy = false;
    }
  };

  const cancel = () => {
    if (state.busy) return;
    state.open = false;
    action = null;
  };

  return { state, ask, confirm, cancel };
};
