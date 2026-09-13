export type PasswordValidation = { valid: true } | { valid: false; reason: string };

export function validateOwnerPassword(password: string): PasswordValidation {
  if (password.length < 8) {
    return { valid: false, reason: "Password must be at least 8 characters." };
  }

  if (!/[A-Z]/.test(password)) {
    return { valid: false, reason: "Password must include an uppercase letter." };
  }

  if (!/[a-z]/.test(password)) {
    return { valid: false, reason: "Password must include a lowercase letter." };
  }

  if (!/[0-9]/.test(password)) {
    return { valid: false, reason: "Password must include a number." };
  }

  return { valid: true };
}
