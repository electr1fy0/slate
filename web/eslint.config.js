//  @ts-check

import { tanstackConfig } from "@tanstack/eslint-config"

export default [
  ...tanstackConfig,
  {
    rules: {
      "import/no-cycle": "off",
      "import/order": "off",
      "sort-imports": "off",
      "@typescript-eslint/array-type": "off",
      "@typescript-eslint/require-await": "off",
      "pnpm/json-enforce-catalog": "off",
    },
  },
  {
    // Vendored shadcn/ui components — keep pristine so upstream `add` can overwrite.
    files: ["src/components/ui/**"],
    rules: {
      "@typescript-eslint/no-unnecessary-condition": "off",
      "no-shadow": "off",
    },
  },
  {
    ignores: ["eslint.config.js", ".prettierrc", "src/routeTree.gen.ts"],
  },
]
