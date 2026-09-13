import nextCoreWebVitals from "eslint-config-next/core-web-vitals";
import nextTypescript from "eslint-config-next/typescript";

const config = [
  ...nextCoreWebVitals,
  ...nextTypescript,
  {
    ignores: [
      ".next/**",
      "coverage/**",
      "playwright-report/**",
      "test-results/**",
      "target/**",
      ".scout-data/**",
    ],
  },
  {
    rules: {
      "no-console": ["warn", { allow: ["error", "warn"] }],
      "@typescript-eslint/no-explicit-any": "error",
    },
  },
];

export default config;
