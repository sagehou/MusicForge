import type { Config } from "tailwindcss";

export default {
  content: ["./index.html", "./src/**/*.{ts,tsx}"],
  theme: { extend: { colors: { background: "#0c1017", foreground: "#e8eef7", primary: "#70e0b3", muted: "#8a98ad", border: "#263143" } } },
  plugins: [],
} satisfies Config;
