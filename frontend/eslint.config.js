// @ts-check
const eslint = require('@eslint/js');
const tseslint = require('typescript-eslint');
const angular = require('angular-eslint');

module.exports = tseslint.config(
  {
    // Ignore build outputs and vendored files
    ignores: ['dist/**', '../cmd/mediahub/frontend_embed/**', 'coverage/**', 'node_modules/**'],
  },
  {
    files: ['**/*.ts'],
    extends: [
      eslint.configs.recommended,
      ...tseslint.configs.recommended,
      ...angular.configs.tsRecommended,
    ],
    processor: angular.processInlineTemplates,
    rules: {
      '@angular-eslint/directive-selector': [
        'error',
        {
          type: 'attribute',
          // 'secureSrc' predates the 'app' prefix convention (see secure-image.directive.ts)
          prefix: ['app', 'secure'],
          style: 'camelCase',
        },
      ],
      '@angular-eslint/component-selector': [
        'error',
        {
          type: 'element',
          prefix: 'app',
          style: 'kebab-case',
        },
      ],

      // ---------------------------------------------------------------------------
      // Baseline (Step 0 of AI/Reports/Roadmap.md): the codebase predates linting,
      // so the rules below fire on existing code and are kept at "warn" to give a
      // usable red/green signal without code changes. Ratchet them back to "error"
      // as the corresponding roadmap items land:
      //   * prefer-inject, prefer-standalone, no-explicit-any  -> Step 6 (FE-042)
      //   * no-unused-vars, no-empty, prefer-const,
      //     no-prototype-builtins                              -> Step 6 (FE-039/040)
      //   * template a11y rules                                -> Step 7 (FE-044-054)
      // ---------------------------------------------------------------------------
      '@angular-eslint/prefer-inject': 'warn',
      '@typescript-eslint/no-explicit-any': 'warn',
      '@typescript-eslint/no-unused-vars': 'warn',
      'no-empty': 'warn',
      'prefer-const': 'warn',
      'no-prototype-builtins': 'warn',
      // The project intentionally uses NgModules (see "schematics": standalone:false
      // in angular.json); this rule contradicts the project convention.
      '@angular-eslint/prefer-standalone': 'off',
    },
  },
  {
    files: ['**/*.html'],
    extends: [
      ...angular.configs.templateRecommended,
      ...angular.configs.templateAccessibility,
    ],
    rules: {
      // Pre-existing a11y debt, cleaned up in Step 7 (FE-044-FE-054).
      '@angular-eslint/template/label-has-associated-control': 'warn',
      '@angular-eslint/template/click-events-have-key-events': 'warn',
      '@angular-eslint/template/interactive-supports-focus': 'warn',
      '@angular-eslint/template/no-negated-async': 'warn',
      '@angular-eslint/template/alt-text': 'warn',
    },
  }
);
