import js from "@eslint/js";
import prettier from "eslint-config-prettier";
import reactHooks from "eslint-plugin-react-hooks";
import reactRefresh from "eslint-plugin-react-refresh";
import globals from "globals";
import tseslint from "typescript-eslint";

export default tseslint.config(
  {
    ignores: [
      "dist",
      "node_modules",
      "src/components/ui",
      "e2e/out",
      "e2e/test-results",
      "e2e/report",
    ],
  },
  {
    files: ["**/*.{ts,tsx}"],
    extends: [
      js.configs.recommended,
      ...tseslint.configs.strictTypeChecked,
      ...tseslint.configs.stylisticTypeChecked,
      reactHooks.configs.flat["recommended-latest"],
      prettier,
    ],
    languageOptions: {
      ecmaVersion: 2022,
      globals: globals.browser,
      parserOptions: {
        projectService: true,
        tsconfigRootDir: import.meta.dirname,
      },
    },
    rules: {
      "@typescript-eslint/consistent-type-imports": "error",
      // The style guide uses type aliases, including for component props.
      "@typescript-eslint/consistent-type-definitions": "off",
    },
  },
  {
    files: ["src/**/*.tsx"],
    plugins: { "react-refresh": reactRefresh },
    rules: {
      "react-refresh/only-export-components": ["warn", { allowConstantExport: true }],
    },
  },
  {
    // The design tokens in src/index.css are the only source of colour, type
    // size, and radius, and icons come in three sizes (docs/STYLE_GUIDE.md,
    // Web UI design). src/components/ui is generated and ignored above.
    files: ["src/**/*.tsx"],
    rules: {
      "no-restricted-syntax": [
        "error",
        ...["Literal[value", "TemplateElement[value.raw"].flatMap((node) => [
          {
            selector: `${node}=/\\b(bg|text|border|ring|fill|stroke|from|to|via|outline|decoration|divide|shadow)-(red|orange|amber|yellow|lime|green|emerald|teal|cyan|sky|blue|indigo|violet|purple|fuchsia|pink|rose|slate|gray|zinc|neutral|stone)-\\d/]`,
            message:
              "Use a theme colour token (primary, muted, success, warning, destructive, …), not a Tailwind palette colour.",
          },
          {
            selector: `${node}=/(text|bg|border)-\\[(#|rgb|hsl|oklch|\\d)/]`,
            message:
              "Use a theme token or the type scale (text-2xs … text-base), not an arbitrary colour or size.",
          },
          {
            selector: `${node}=/(^|[\\s:])rounded(-[trblse]{1,2})?(-(none|xs|sm|lg|xl|[234]xl))?(\\s|$)|rounded(-[trblse]{1,2})?-\\[/]`,
            message:
              "Use rounded-md for a box and rounded-full for a pill; index.css maps every corner to --radius.",
          },
          {
            selector: `${node}=/(^|[\\s:])shadow(-(2xs|xs|sm|md|lg|xl|2xl))?(\\s|$)/]`,
            message:
              "Shadows belong to floating layers (dialogs, popovers), which the components/ui primitives draw.",
          },
        ]),
        ...["Literal[value", "TemplateElement[value.raw"].map((node) => ({
          selector: `JSXOpeningElement[name.name=/^[A-Z]/]:has(JSXAttribute[name.name="aria-hidden"]) > JSXAttribute[name.name="className"] ${node}=/(^|\\s)size-(?!(3|3\\.5|4)(\\s|$))/]`,
          message:
            "An icon is size-4, size-3.5 in a small control or beside text-xs, or size-3 in an xs control or beside text-2xs.",
        })),
      ],
    },
  },
);
