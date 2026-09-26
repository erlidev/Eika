/** Wire types for signing in. */

/** AuthStatus is the body of GET /api/auth/status. */
export type AuthStatus = {
  password_set: boolean;
};

/** SignIn is a new session: the bearer token and when it expires. */
export type SignIn = {
  token: string;
  expires_at: string;
};
