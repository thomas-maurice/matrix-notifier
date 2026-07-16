// Thin wrapper over vue3-toastify so components have one import for
// operational feedback (top-right toasts, no DOM shifting).
import { toast } from 'vue3-toastify'

export function notifyError(message) {
  toast.error(message || 'Something went wrong')
}

export function notifySuccess(message) {
  toast.success(message)
}
