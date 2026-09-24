function requireEnv(name: keyof ImportMetaEnv): string {
  const value = import.meta.env[name];
  if (!value) {
    throw new Error(`Missing ${name}. Set it in ui/.env.${import.meta.env.MODE}.`);
  }
  return value;
}

export const config = {
  backendUrl: requireEnv("VITE_BACKEND_URL"),
  googleClientId: requireEnv("VITE_GOOGLE_CLIENT_ID"),
};
