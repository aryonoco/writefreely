# Feature Inventory & Layer Architecture (v5)

## Layers

The proposed layer order is: `base`, `typography`, `theme`, `elements`, `utilities`, `enhancements`, `adaptations`. Styles outside these named layers (Pre Layers) will have the highest precedence before `!important` styles within layers.

### main.css: Pre Layers (Highest Specificity - Outside Named Layers)

**Role & Description**: Cascading and Inheritance Level 5
Focuses on atomic resets and emergency overrides using `!important` to combat third-party style pollution (like WriteFreely's defaults and highlight.js). Implements continuous keyframe-based reset animations and box model normalization. Directly addresses CSSWG's "Origin + Importance" cascade layer requirements through vendor style quarantine and WriteFreely-specific element obliteration. The nuclear reset system follows CSS Cascading Level 5 for origin precedence management.

#### 1. Nuclear Reset System

*   Universal `!important` overrides for all elements/pseudo-elements (`*`, `*::before`, `*::after`), including animation/transition blocking.
*   Grid system obliteration (resetting `grid-template-columns`/`rows`).
*   Targeted `all: initial !important` resets for specific WriteFreely elements (`body`, `#post`, `#collection`, `.e-content`, `#blog-title`, `header`, `nav`, `footer`, `.content`, `#wrapper`, `.posts`, `.post`, `.meta`, `input`, `textarea`, `button`).
*   Highlight.js theme obliteration (`.hljs`, `[class^="hljs-"]`, `[class*=" hljs-"]`).
*   Continuous keyframe-based reset animation system (`continuously-reset`) with delay control (`--continuously-reset-delay`).
*   Box model normalization (`box-sizing: border-box !important`).
*   Base typography resets (`text-rendering: optimizeLegibility !important`, `text-size-adjust: 100% !important`).

#### 2. Core System Variables (Protected)

*   Defines essential variables required *before* any layered calculations or styles. These are foundational constants, thresholds, and definitions needed early in the cascade. Font stacks retain `!important` for protection.

```css
:root {
  /* --- Essential Metrics & Constants --- */
  /* 1. Optical Size Base (CSS Fonts Level 5 §2.1) */
  /* WCAG 3.0 §3.4.7 recommendation */
  --optical-size: clamp(1rem, calc(1rem + 0.15dvw), min(1.25rem, 10dvh));

  /* 2. Logarithmic Scale Constant (For calc() in @typography) */
  --heading-scale: 1.333; /* Represents ~Major Third scale */

  /* 3. Layout Constants */
  /* Defines safe area padding based on device insets */
  --content-safe-area-top: env(safe-area-inset-top, 0px);
  --content-safe-area-right: env(safe-area-inset-right, 0px);
  --content-safe-area-bottom: env(safe-area-inset-bottom, 0px);
  --content-safe-area-left: env(safe-area-inset-left, 0px);

  /* Base ratio (unitless) */
  --line-height-base-ratio: 1.5;

  /* --- Font System --- */
  /* 4. Font Stack Definitions (Protection for resets) */
  --font-sans: "Inclusive Sans", system-ui, ui-sans-serif, sans-serif !important; /* Define primary stacks */
  --font-serif: "Source Serif 4", ui-serif, serif !important;
  --font-mono: "Fira Code", ui-monospace, monospace !important;

  /* 5. Font Feature Controls (Defaults needed early) */
  --font-features-sans: "kern" on, "liga" on, "calt" on;
  --font-features-serif: "kern" on, "liga" on, "dlig" on, "onum" on, "salt" on;
  --font-features-mono: "ss01" on, "ss03" on, "zero" on;

  /* --- Accessibility & Color Foundation --- */
  /* 6. WCAG 3.0 Draft APCA Threshold Values (Lc) */
  /* Referenced by @adaptations, potentially overridden in @theme */
  --wcag3-body-large: 60;
  --wcag3-body-normal: 75;
  --wcag3-body-small: 90;
  --wcag3-heading-large: 55;
  --wcag3-heading-normal: 65;
  --wcag3-heading-small: 75;
  --wcag3-ui-large: 40;
  --wcag3-ui-normal: 60;
  --wcag3-ui-small: 75;
  --wcag3-non-text-normal: 30;
  --wcag3-disabled-text: 45;
  --wcag3-placeholder: 50;


  /* --- Base Context Defaults (Used if not overridden later) --- */
  /* Primarily managed contextually in @adaptations */
  --current-font-family: var(--font-sans);
  --current-font-weight: 400; /* Default base weight */
  --current-line-height: var(--line-height-base-ratio);
  --current-letter-spacing: 0em;
  --text-alpha: 0.9; /* Default text alpha */
}
```

##### **Decision Matrix: What Belongs Pre-Layer (Revised)**

| **Characteristic**                    | **Pre-Layer** | **Layer**              | **Why**                                                                          |
| :------------------------------------ | :------------ | :--------------------- | :------------------------------------------------------------------------------- |
| Used directly in Nuclear Reset        | ✅            | ❌                     | Reset styles depend on these values (e.g., font stacks).                         |
| Foundational Metrics (`--optical-size`) | ✅            | ❌                     | Essential for subsequent calculations across layers.                           |
| Font Stack Definitions                | ✅            | ❌                     | Reset's `font-family` overrides need these defined first.                       |
| Base Font Feature Defaults            | ✅            | ❌                     | Default settings needed before specific application in `@typography`.           |
| WCAG Thresholds (Lc Values)           | ✅            | ❌                     | Constants needed by APCA delta calculations in `@adaptations`.                   |
| Root Layout/Environment Metrics       | ✅            | ❌                     | `env(safe-area-inset-*)` needs early definition.                                 |
| Animation Timing Controls             | ✅            | ❌                     | Continuous reset system (if used) depends directly on these.                    |
| **APCA Parameters**                   | ❌            | ✅ (`@adaptations`)    | Core math constants used *only* within the `@adaptations` calculation layer.    |
| **APCA Delta Calculations**           | ❌            | ✅ (`@adaptations`)    | Depend on APCA parameters and WCAG thresholds; calculate within `@adaptations`. |
| **APCA Minimum Lightness Calcs**      | ❌            | ✅ (`@adaptations`)    | Depend on deltas and `--parent-bg-l` (set in `@theme`/`@elements`).             |
| Primary Color Palette Definition      | ❌            | ✅ (`@base`)           | Defines the base color tokens (OKLCH L/C/H values).                              |
| Color Derivatives / Semantic Colors   | ❌            | ✅ (`@adaptations`)    | Calculated/derived from the primary palette and APCA logic in `@adaptations`.  |
| Component-Specific Variables          | ❌            | ✅ (`@elements`, etc.) | Variables specific to certain UI components or layers.                           |
| Typographic Scale Variables (`--h*`)  | ❌            | ✅ (`@typography`)     | Calculated using Pre-Layer constants but defined and used within `@typography`. |
| Adaptive Font Weight Variables        | ❌            | ✅ (`@typography`)     | Calculated using base weights but defined and used within `@typography`.          |
| Background Propagation Listener       | ❌            | ✅ (`@adaptations`)    | Needs to update `--parent-bg-l` used by calculations within `@adaptations`.      |

---

##### **Key Determination Factors (Revised)**

1.  **Reset Dependency**: Variables directly referenced in nuclear reset styles (`*`, targeted resets).
2.  **Calculation Order & Layer Dependency**: Values required *before* any layered calculations can occur reside pre-layer. Values needed for calculations *within* a specific layer (like APCA params for `@adaptations`) or dependent on variables set in *earlier* layers (like min lightness needing `--parent-bg-l` from `@theme`/`@elements`) belong in the *later* layer where they are calculated or used, respecting Cascade Layers L5 §4.3.
3.  **Persistent Governors**: Control mechanisms that must reliably persist (e.g., animation timing).
4.  **Environment Variables**: `env()` values needing early resolution.
5.  **Cross-Layer Constants**: Foundational values used across multiple subsequent layers (e.g., `--optical-size`).
6.  **Font Loading Priority**: Font stack definitions needed before first paint.
7.  **Override Necessity**: Needs `!important` protection against third-party styles (primarily font stacks).

---

### 1. @base

**Role & Description**: CSS Color Module Level 5 (OKLCH), Base Design Tokens.
Foundation layer defining core design tokens, primarily the Rosé Pine color palette using OKLCH, base semantic color components, base font metrics, and base layout/spacing values. Does *not* perform APCA calculations or apply styles directly.

```css
@layer base {
  /* Best practice: Register semantic custom properties */
  /* Example - Add for key variables used across layers */
  /* @property --font-weight-base { syntax: '<integer>'; inherits: true; initial-value: 400; } */

  :root {
    /* === Rosé Pine Color Palette (OKLCH Components) === */
    /* Using original names */
    --rp-base-l: 0.95; --rp-base-c: 0.0126; --rp-base-h: 71.33;
    --rp-surface-l: 0.99; --rp-surface-c: 0.0091; --rp-surface-h: 76.61;
    --rp-overlay-l: 0.94; --rp-overlay-c: 0.0174; --rp-overlay-h: 73.07;
    --rp-muted-l: 0.67; --rp-muted-c: 0.0267; --rp-muted-h: 298.62;
    --rp-subtle-l: 0.58; --rp-subtle-c: 0.0448; --rp-subtle-h: 291.04;
    --rp-text-l: 0.46; --rp-text-c: 0.0625; --rp-text-h: 289.83;
    --rp-love-l: 0.6; --rp-love-c: 0.1061; --rp-love-h: 2.62;
    --rp-gold-l: 0.76; --rp-gold-c: 0.1457; --rp-gold-h: 70;
    --rp-rose-l: 0.7; --rp-rose-c: 0.1054; --rp-rose-h: 23.36;
    --rp-pine-l: 0.5; --rp-pine-c: 0.0775; --rp-pine-h: 227.7;
    --rp-foam-l: 0.63; --rp-foam-c: 0.0664; --rp-foam-h: 210.06;
    --rp-iris-l: 0.62; --rp-iris-c: 0.0723; --rp-iris-h: 305.67;
    --rp-highlight-low-l: 0.95; --rp-highlight-low-c: 0.0105; --rp-highlight-low-h: 58.21;
    --rp-highlight-med-l: 0.89; --rp-highlight-med-c: 0.0061; --rp-highlight-med-h: 31.06;
    --rp-highlight-high-l: 0.84; --rp-highlight-high-c: 0.0061; --rp-highlight-high-h: 334;

    /* === Combined OKLCH Color Variables === */
    --rp-base: oklch(var(--rp-base-l) var(--rp-base-c) var(--rp-base-h));
    --rp-surface: oklch(var(--rp-surface-l) var(--rp-surface-c) var(--rp-surface-h));
    --rp-overlay: oklch(var(--rp-overlay-l) var(--rp-overlay-c) var(--rp-overlay-h));
    --rp-muted: oklch(var(--rp-muted-l) var(--rp-muted-c) var(--rp-muted-h));
    --rp-subtle: oklch(var(--rp-subtle-l) var(--rp-subtle-c) var(--rp-subtle-h));
    --rp-text: oklch(var(--rp-text-l) var(--rp-text-c) var(--rp-text-h));
    --rp-love: oklch(var(--rp-love-l) var(--rp-love-c) var(--rp-love-h));
    --rp-gold: oklch(var(--rp-gold-l) var(--rp-gold-c) var(--rp-gold-h));
    --rp-rose: oklch(var(--rp-rose-l) var(--rp-rose-c) var(--rp-rose-h));
    --rp-pine: oklch(var(--rp-pine-l) var(--rp-pine-c) var(--rp-pine-h));
    --rp-foam: oklch(var(--rp-foam-l) var(--rp-foam-c) var(--rp-foam-h));
    --rp-iris: oklch(var(--rp-iris-l) var(--rp-iris-c) var(--rp-iris-h));
    --rp-highlight-low: oklch(var(--rp-highlight-low-l) var(--rp-highlight-low-c) var(--rp-highlight-low-h));
    --rp-highlight-med: oklch(var(--rp-highlight-med-l) var(--rp-highlight-med-c) var(--rp-highlight-med-h));
    --rp-highlight-high: oklch(var(--rp-highlight-high-l) var(--rp-highlight-high-c) var(--rp-highlight-high-h));

    /* === Base Semantic Color Component Assignments === */
    /* Default components used in final color calculation in @adaptations */
    --text-c: var(--rp-text-c);
    --text-h: var(--rp-text-h);
    --code-c: var(--rp-foam-c);
    --code-h: var(--rp-foam-h);
    --heading-c: var(--rp-iris-c);
    --heading-h: var(--rp-iris-h);
    --blockquote-c: var(--rp-muted-c);
    --blockquote-h: var(--rp-muted-h);
    /* Note: --text-main-contrast and --text-secondary-contrast removed as they are no longer used */

    /* === Base Font Metrics === */
    --font-weight-base: 400;
    --font-weight-heading: 600;
    --text-size-large: 1.5rem; 
    --text-size-normal: 1.125rem; 
    
/* === Base Typography Controls (Line Height, Spacing) === */
        /* Dynamic line-height calculations using em */
        --line-height-body: calc(
            var(--line-height-base-ratio) *
                (1 + 0.15 * (1 - var(--optical-size) / 1rem))
        );
        --line-height-heading: calc(var(--line-height-base-ratio) * 0.85);
        --line-height-code: calc(var(--line-height-base-ratio) * 1.2);
    --letter-spacing-base: -0.01em;
    --letter-spacing-heading: -0.03em;
    --letter-spacing-min: 0.01em;
    --letter-spacing-max: 0.06em;
    --letter-spacing-ratio: 0.0025; /* 0.25% of font size */

    /* === Base Spacing and Layout === */
    --block-margin: 1.5em;
    --paragraph-margin: 0.8em;

    /* === Base Justification System Values === */
    --text-justification: unsafe;
    --justification-threshold: 50ch;
    --rag-adjustment: clamp( calc(var(--optical-size) * 0.15), calc(var(--optical-size) * 0.25), 0.4rem );
    --text-indent-base: clamp( 0.25rem, calc(var(--optical-size) * 0.5), 1rem );

    /* === Base Transition Timing === */
    --text-transition-base: 0.3s;
    --text-transition-duration: var(--text-transition-base); /* Default duration */
  }
}
```

---

### 2. @typography

**Role & Description**: CSS Fonts Level 4/5 (Optical Sizing, Variations), CSS Text Level 4 (Text Processing).
Manages all aspects of typography: scaling calculations, applying font features and adaptive weights, setting contextual spacing/layout, and handling text processing like justification and wrapping.

```css
@layer typography {
  :root {
    /* === Typographic Scale Calculation === */
    /* Calculate --h* variables using pre-layer --optical-size and --heading-scale */
    --h6: calc(var(--optical-size) * var(--heading-scale));
    --h5: calc(var(--h6) * var(--heading-scale));
    --h4: calc(var(--h5) * var(--heading-scale));
    --h3: calc(var(--h4) * var(--heading-scale));
    --h2: calc(var(--h3) * var(--heading-scale));
    --h1: calc(var(--h2) * var(--heading-scale));

    /* === Adaptive Font Weight Calculation === */
    /* Normalize font size relative to 1rem for calculations */
    --font-size-normalized: calc(var(--optical-size) / 1rem);
    /* Calculate adaptive weights based on base weights and font size */
    --font-weight-sans-adaptive: clamp( 300, round( nearest, calc(var(--font-weight-base) * var(--font-size-normalized, 1) * 0.9), 100 ), 700 );
    --font-weight-serif-adaptive: clamp( 500, round( nearest, calc( var(--font-weight-heading) * var(--font-size-normalized, 1) * 1.1 ), 100 ), 800 );
    --font-weight-mono-adaptive: clamp( 300, round( nearest, calc(var(--font-weight-base) * var(--font-size-normalized, 1)), 100 ), 700 );
  }

  /* === Base Typography Application === */
  /* Apply base font settings to body/html */
  body, html {
    font-family: var(--font-sans);
    font-size: var(--optical-size);
    font-weight: var(--font-weight-base);
    font-optical-sizing: auto;
    font-synthesis: none;
    line-height: var(--line-height-body);
    letter-spacing: var(--letter-spacing-base);
    font-feature-settings: var(--font-features-sans);
    font-variant-numeric: lining-nums proportional-nums; /* Default */
    text-wrap: pretty; /* Default text wrap */
  }

  /* === Heading Typography Application === */
  h1, h2, h3, h4, h5, h6 {
    font-family: var(--font-serif);
    font-weight: var(--font-weight-heading); /* Base heading weight */
    line-height: var(--line-height-heading);
    letter-spacing: var(--letter-spacing-heading);
    font-feature-settings: var(--font-features-serif);
    font-variant-numeric: oldstyle-nums proportional-nums; /* Heading numerals */
    text-wrap: balance; /* Balance headings */
  }
  h1 { font-size: var(--h1); }
  h2 { font-size: var(--h2); }
  h3 { font-size: var(--h3); }
  h4 { font-size: var(--h4); }
  h5 { font-size: var(--h5); }
  h6 { font-size: var(--h6); }

  /* === Code Typography Application === */
  code, pre, kbd, samp {
    font-family: var(--font-mono);
    line-height: var(--line-height-code);
    letter-spacing: 0; /* No extra letter spacing for mono */
    font-feature-settings: var(--font-features-mono);
    font-variant-numeric: tabular-nums slashed-zero; /* Mono features */
  }

  /* === Text Processing & Layout Application === */
  /* Apply base margins */
  p, ul, ol, dl, figure, blockquote, pre {
    margin-block-start: 0;
    margin-block-end: var(--paragraph-margin);
  }
  h1, h2, h3, h4, h5, h6 {
    margin-block-start: var(--block-margin);
    margin-block-end: var(--paragraph-margin);
  }

  /* Base Justification/Wrapping */
  p, li, dd, dt {
    text-align: start;
    text-justify: auto;
    hyphens: manual;
    overflow-wrap: break-word;
    hanging-punctuation: first allow-end;
    text-indent: var(--text-indent-base);
    word-spacing: 0.05em;
  }

  /* Adaptive Letter Spacing */
  * {
    /* Apply generally, refine per element type if needed */
    letter-spacing: clamp( var(--letter-spacing-min), calc(var(--optical-size) * var(--letter-spacing-ratio)), var(--letter-spacing-max) );
  }
}
```

---

### 3. @theme

**Role & Description**: Media Queries Level 5 (Dark Mode), CSS Color Adjustment Module Level 1.
Handles visual theming, primarily dark mode adaptation by overriding base color lightness *or WCAG thresholds*, and applying semantic background colors to major layout areas.

```css
@layer theme {
  /* === Dark Mode Overrides === */
  @media (prefers-color-scheme: dark) {
    :root {
      /* Set color-scheme for UA consistency */
      color-scheme: dark;

      /* METHOD 1: Adjust base OKLCH lightness values from @base */
      --rp-base-l: 0.15;
      --rp-surface-l: 0.12;
      --rp-overlay-l: 0.18;
      /* --rp-text-l: ...; // Optionally adjust base text lightness */

      /* METHOD 2 (Alternative/Combined): Adjust WCAG thresholds from Pre-Layers */
      /* This indirectly increases contrast deltas calculated in @adaptations */
      /* Use EITHER Method 1 OR Method 2 for primary adjustment, avoid both unless carefully managed */
      /* --wcag3-body-normal: 90 !important;  */ /* Increase target Lc */
      /* --wcag3-heading-normal: 80 !important; */
    }
  }

  @media (prefers-color-scheme: light) {
    :root {
      color-scheme: light;
    }
  }


  /* === Semantic Background Mapping === */
  /* Apply backgrounds using @base variables */
  /* Set --element-bg-l for APCA context */
  body, html {
    background-color: var(--rp-base);
    --element-bg-l: var(--rp-base-l);
    --parent-bg-l: var(--rp-base-l); /* Initialize parent bg */
  }
  header {
    background-color: var(--rp-surface);
    --element-bg-l: var(--rp-surface-l);
  }
  footer {
    background-color: var(--rp-overlay);
    --element-bg-l: var(--rp-overlay-l);
  }
  nav {
    background-color: var(--rp-subtle);
    --element-bg-l: var(--rp-subtle-l);
  }
  aside {
    background-color: var(--rp-muted);
    --element-bg-l: var(--rp-muted-l);
  }

  /* Section Alternation Example */
  main > section:nth-child(odd) {
    background-color: var(--rp-surface);
    --element-bg-l: var(--rp-surface-l);
    padding: 2em; /* Example padding */
  }
  main > section:nth-child(even) {
    background-color: var(--rp-overlay);
    --element-bg-l: var(--rp-overlay-l);
    padding: 2em; /* Example padding */
  }
}
```

---

### 4. @elements

**Role & Description**: Component Styling, Layout Patterns, Vendor Overrides. CSS Display Level 3 (Containment), CSS Overflow Level 4.
Styles specific HTML elements and common UI patterns (cards, containers), including targeted overrides for WriteFreely elements not covered by the nuclear reset or base typography/theme layers. Handles element-specific visual details like borders, padding, shadows.

```css
@layer elements {
  /* === Base Element Styling (Refinements) === */
  ul, ol { padding-inline-start: 1.5em; /* Basic list indent */ }
  li { margin-block: 0.5em 0; }
  table { /* Basic table styles */ }
  hr { /* Basic hr styles */ }

  /* === Component Styling === */

  /* Code Blocks */
  pre {
    padding: 1em;
    border-radius: 0.5em;
    /* Background calculated in @adaptations using --code-block-bg-l */
    background-color: oklch(var(--code-block-bg-l, 0.2) 0.03 240);
    border: 0.0625em solid oklch(clamp(0.4, calc(var(--parent-bg-l, 0.95) + 0.1), 0.8) 0.05 290 / 0.4); /* Original border */
    overflow-x: auto;
    font-variant-ligatures: none; /* Disable code ligatures in block */
  }
  pre code {
    /* Base styles from @typography */
    font-size: 0.85em; /* Relative size from original */
    padding: 0;
    background: none;
    border: none;
    white-space: pre; /* Ensure pre formatting preserved */
    /* Color applied in @adaptations via --code-contrast */
  }
  code:not(pre code) { /* Inline code */
    /* Background calculated in @adaptations using inline --code-bg-l */
    background: oklch(var(--code-bg-l, 0.9) 0.05 290 / 0.15);
    padding: 0.2em 0.4em;
    border-radius: 0.25em;
    font-size: 0.9em; /* Relative size from original */
    /* Color applied in @adaptations via --code-contrast */
  }

  /* Blockquotes */
  blockquote {
    padding-inline-start: 1em;
    /* Border color uses --blockquote-contrast from @adaptations */
    border-inline-start: 0.25em solid var(--blockquote-contrast, var(--rp-muted));
    opacity: 0.9;
    font-style: italic; /* Applied here, not via context var */
    word-spacing: -0.03em; /* From original */
    /* Context vars (--current-*, --text-alpha) set in @adaptations */
  }
  blockquote p, blockquote li { /* Nested margins */
    margin-block: clamp(0.25em, 1lh, 0.75em);
  }
  blockquote p:first-child, blockquote li:first-child { margin-block-start: 0; }
  blockquote p:last-child, blockquote li:last-child { margin-block-end: 0; }


  /* Headings (Visuals) */
  h1, h2, h3 {
    /* Example: Add variation settings */
    font-variation-settings: "wght" var(--font-weight-serif-adaptive), "GRAD" 88;
  }
  /* Any additional visual styles for headings */

  /* Lists (Enhanced - from original) */
  li {
      text-indent: calc(var(--text-indent-base) * -1);
      padding-inline-start: calc(var(--optical-size) * 1.25);
  }
  li li { /* Nested lists */
      text-indent: calc(var(--text-indent-base) * -0.75);
      padding-inline-start: var(--optical-size);
  }


  /* Form Controls */
  button, input, select, textarea {
    /* Basic normalization if needed beyond reset */
    /* Appearance, padding, borders */
    font-family: inherit; /* Ensure form elements inherit font */
    font-size: inherit;
    line-height: inherit;
    color: inherit; /* Start with inherited color, overridden by @adaptations */
  }
  label { /* Styling for labels */ }

  /* === Layout Patterns === */
  .card, [class*="card"],
  .container, [class*="container"],
  .box, [class*="box"] {
    background-color: var(--rp-surface); /* Default surface bg */
    --element-bg-l: var(--rp-surface-l);
    padding: 1.5em;
    border-radius: 0.5em;
    /* Shadow uses --shadow-contrast-l from @adaptations */
    box-shadow: 0 0.125em 0.5em oklch( var(--shadow-contrast-l, 0.1) 0.02 270 / clamp(0.1, calc(1 - var(--parent-bg-l, 0.95)), 0.3) );
  }

  /* === WriteFreely Specific Overrides === */
  /* Fine-tuning specific WF elements if base/component styles aren't enough */
  /* Example: */
  /* body#post article:not(.norm) { ... } */
  /* #blog-title a { ... } */
  /* input#title.norm { ... } */
  /* .e-content a { ... } */
  /* The original had many specific font overrides for WF elements. These are likely */
  /* unnecessary now due to the robust base typography and context setting in @adaptations, */
  /* but could be added back here if needed for fine-tuning. */

}
```

---

### 5. @utilities

**Role & Description**: Responsive Design, Accessibility Enhancements. CSS Conditional Rules Level 5 (Container Queries), CSS Viewport Units Level 4, Media Queries Level 5.
Provides utility styles, responsive adjustments via media/container queries, and core accessibility features like focus visibility and reduced motion.

```css
@layer utilities {
  /* === Responsive Design System === */
  /* Adjust layout/spacing/typography variables based on breakpoints */
  @media (min-width: 40em) {
    :root { /* Or specific selectors */
      --rag-adjustment: clamp( calc(var(--optical-size) * 0.2), calc(var(--optical-size) * 0.3), 0.5rem );
    }
    p, li { /* Enable auto hyphens, adjust indent */
      hyphens: auto;
      --text-indent-base: clamp( 0rem, var(--text-indent-base), 1.5rem );
    }
    /* Adjust blockquote margins for rag adjustment */
    blockquote {
      margin-inline: calc(var(--rag-adjustment) * -1);
      padding-inline: clamp(0.5rem, var(--rag-adjustment), 1.5rem);
      max-inline-size: calc(100% - (var(--rag-adjustment) * 2));
    }
  }
  @media (min-width: 64em) {
    h1,h2,h3,h4,h5,h6 { text-align: start; text-justify: auto; }
    p, li { /* Justify text on larger screens */
      text-indent: 0;
      text-align: justify;
      text-justify: inter-word;
    }
  }

  /* Container Query Setup (Example) */
  .container {
    container-type: inline-size;
    container-name: main-container;
  }
  /* @container main-container (min-width: ...) { ... } */

  /* === Accessibility Core === */

  /* Focus Visibility */
  /* Uses --focus-indicator-l calculated in @adaptations */
  a:focus-visible,
  button:focus-visible,
  input:focus-visible,
  select:focus-visible,
  textarea:focus-visible,
  /* WriteFreely specific targets */
  .e-content a:focus-visible,
  nav a:focus-visible,
  #blog-title a:focus-visible {
    outline: 0.2em solid oklch(var(--focus-indicator-l, 0.6) var(--rp-love-c) var(--rp-love-h)); /* Use Love color */
    outline-offset: 0.2em;
  }

  /* Reduced Motion */
  @media (prefers-reduced-motion: no-preference) {
    html { scroll-behavior: smooth; }
    /* Define base transitions here or in @enhancements */
  }
  @media (prefers-reduced-motion: reduce) {
    *, *::before, *::after {
      animation-duration: 0.01ms !important;
      animation-iteration-count: 1 !important;
      transition-duration: 0.01ms !important;
      scroll-behavior: auto !important;
    }
  }

  /* Image Contrast Filter (For images of text - experimental) */
  img[alt]:not([alt=""]) {
    /* Filter needs access to min-lightness, calculated in @adaptations */
    /* filter: contrast( calc(var(--parent-bg-l) * 1.8 - var(--body-text-min-l) * 0.8) ); */
    /* Revisit this calculation if needed, complex dependency */
  }

  /* SVG Styling */
  svg:not([aria-hidden="true"]) {
    /* Color applied in @adaptations via --ui-text-contrast or similar */
    /* color: var(--ui-text-contrast); */
  }

  /* Form Validation/State Styling */
  :invalid {
    /* Use Love color components from @base */
    --text-c: var(--rp-love-c);
    --text-h: var(--rp-love-h);
    /* Target Lc set contextually in @adaptations */
    /* Apply border or background if desired */
    /* Example: border-color: var(--ui-text-contrast); */
  }
  /* Context for :disabled and ::placeholder set in @adaptations */

}
```

---

### 6. @enhancements

**Role & Description**: Performance Optimization, Progressive Enhancement Fallbacks. CSS Containment Level 3, CSS Fonts Level 4 (Font Display).
Focuses on optimizing rendering performance and providing fallbacks or enhancements for newer CSS features.

```css
@layer enhancements {
  /* === Optimizations === */

  /* Content Visibility */
  article, section, main {
    content-visibility: auto;
    contain-intrinsic-size: auto 300px; /* Estimate based on typical content */
  }
  header, footer, nav {
    content-visibility: auto;
    contain-intrinsic-size: auto 100px; /* Estimate for smaller sections */
  }

  /* Font Display Strategy (Example if using @font-face) */
  /* @font-face { font-family: 'MyFont'; src: ...; font-display: optional; } */

  /* Transition Timing Control */
  /* Apply transitions using --text-transition-duration from @base */
  p, li, blockquote { /* Example elements */
    transition:
      text-indent var(--text-transition-duration) ease-out,
      hanging-punctuation var(--text-transition-duration) ease-out;
  }
  /* Adjust transition timing based on preferences */
  @media (prefers-contrast: more) {
    :root { --text-transition-duration: calc( var(--text-transition-base) * 0.66 ); }
  }
  @media (prefers-contrast: less) {
    :root { --text-transition-duration: calc( var(--text-transition-base) * 1.33 ); }
  }


  /* === Progressive Enhancement === */

  /* @supports Examples */
  @supports (text-wrap: pretty) { /* Already applied, maybe revert fallbacks */ }
  @supports (text-wrap: balance) { /* Already applied */ }
  @supports (hanging-punctuation: first) { /* Already applied */ }
  /* Add @supports for other features like container queries, subgrid, etc. */

  /* Fallback Definitions (Example) */
  /* body { --text-contrast: #FallbackColor; } */
}
```

---

### 7. @adaptations

**Role & Description**: Dynamic Calculation & Adaptation Engine. CSS Custom Properties Level 1, CSS Values Level 4 (Math Functions), CSS Color Level 5 (Modification).
The core engine for dynamic calculations based on context (background, font size, weight). Handles APCA contrast calculations, minimum lightness enforcement, and applies final dynamic colors. **This layer relies on variables defined in previous layers.** It calculates colors using the direct APCA formula based on contextual semantic targets.

```css
@layer adaptations {
  /* Best Practice: Register calculated semantic properties */
  /* @property --text-contrast { syntax: '<color>'; inherits: true; } */
  /* @property --weight-adjustment-factor { syntax: '<number>'; inherits: true; initial-value: 0; } */

  :root {
    /* === APCA Foundation (Constants needed for calculations in this layer) === */
    --apca-exponent: 2.4;
    --apca-multiplier: 1.14;
    --apca-offset: 0.27;


    /* === APCA Delta Calculations === */
    /* Calculate lightness deltas based on WCAG Lc thresholds from Pre-Layers */
    --body-text-min-delta: calc( pow( var(--wcag3-body-normal) / var(--apca-multiplier), 1 / var(--apca-exponent) ) - var(--apca-offset) );
    --code-text-min-delta: calc( pow( var(--wcag3-body-small) / var(--apca-multiplier), 1 / var(--apca-exponent) ) - var(--apca-offset) );
    --heading-text-min-delta: calc( pow( var(--wcag3-heading-normal) / var(--apca-multiplier), 1 / var(--apca-exponent) ) - var(--apca-offset) );
    --blockquote-text-min-delta: calc( pow( (var(--wcag3-body-normal) * 0.9) / var(--apca-multiplier), 1 / var(--apca-exponent) ) - var(--apca-offset) );
    --ui-text-min-delta: calc( pow( var(--wcag3-ui-normal) / var(--apca-multiplier), 1 / var(--apca-exponent) ) - var(--apca-offset) );
    --non-text-min-delta: calc( pow( var(--wcag3-non-text-normal) / var(--apca-multiplier), 1 / var(--apca-exponent) ) - var(--apca-offset) );
    --focus-indicator-min-delta: calc( pow( 45 / var(--apca-multiplier), 1 / var(--apca-exponent) ) - var(--apca-offset) ); /* 45 Lc for focus */
    --code-block-bg-min-delta: calc( pow( (var(--wcag3-body-small) * 0.6) / var(--apca-multiplier), 1 / var(--apca-exponent) ) - var(--apca-offset) );
    --code-bg-min-delta: calc( pow( (var(--wcag3-body-small) * 0.5) / var(--apca-multiplier), 1 / var(--apca-exponent) ) - var(--apca-offset) ); /* Inline code bg */

    /* === APCA Minimum Lightness Calculations === */
    /* Calculate minimum required lightness based on parent background and delta */
    /* Ensures text is readable even if target lightness is very close to background */
    /* Uses calc() and sign() for directionality */
    --body-text-min-l: clamp( 0.5, calc( var(--parent-bg-l, 0.95) + (var(--body-text-min-delta) * sign(0.5 - var(--parent-bg-l, 0.95))) ), 0.7 );
    --code-text-min-l: clamp( 0.5, calc( var(--parent-bg-l, 0.95) + (var(--code-text-min-delta) * sign(0.5 - var(--parent-bg-l, 0.95))) ), 0.75 );
    --heading-text-min-l: clamp( 0.5, calc( var(--parent-bg-l, 0.95) + (var(--heading-text-min-delta) * sign(0.5 - var(--parent-bg-l, 0.95))) ), 0.72 );
    --blockquote-text-min-l: clamp( 0.45, calc( var(--parent-bg-l, 0.95) + (var(--blockquote-text-min-delta) * sign(0.5 - var(--parent-bg-l, 0.95))) ), 0.68 );
    --ui-text-min-l: clamp( 0.48, calc( var(--parent-bg-l, 0.95) + (var(--ui-text-min-delta) * sign(0.5 - var(--parent-bg-l, 0.95))) ), 0.7 );
    --focus-indicator-l: clamp( 0.5, calc( var(--parent-bg-l, 0.95) + (var(--focus-indicator-min-delta) * sign(0.5 - var(--parent-bg-l, 0.95))) ), 0.9 );
    --shadow-contrast-l: clamp( 0.05, calc( var(--parent-bg-l, 0.95) - var(--non-text-min-delta) ), 0.2 ); /* Shadow lightness */

    /* Code Block Background Lightness Calculation */
    --code-block-bg-l: min(max(
        calc(var(--parent-bg-l, 0.95) + (var(--code-block-bg-min-delta) * sign(0.5 - var(--parent-bg-l, 0.95)))), /* APCA target */
        calc(var(--parent-bg-l, 0.95) + (0.25 * sign(0.5 - var(--parent-bg-l, 0.95)))) /* WCAG floor (0.25 delta) */
      ), 1); /* Capped at 1 (white) */

    /* Inline Code Background Lightness Calculation */
    --code-bg-l: clamp( 0.2, calc( var(--parent-bg-l, 0.95) + (var(--code-bg-min-delta) * sign(0.5 - var(--parent-bg-l, 0.95))) ), 0.95 );


    /* === Contextual Target Lc Calculation === */
    /* Calculate target Lc based on element font size relative to breakpoints */
    --get-size-based-threshold: calc( ( 1 - min( 1, max( 0, (var(--element-font-size, 1rem) - var(--text-size-normal)) / (var(--text-size-large) - var(--text-size-normal)) ) ) ) * (var(--element-threshold-small, var(--wcag3-body-small)) - var(--element-threshold-normal, var(--wcag3-body-normal))) + var(--element-threshold-normal, var(--wcag3-body-normal)) );

    /* Font Weight Compensation (WCAG 3.0 §3.4.9) */
    /* Increases contrast target Lc for weights below 400 */
    --weight-adjustment-factor: max(0, (400 - var(--current-font-weight, 400)) * 0.00075);
    /* Final target Lc, adjusted for weight */
    --final-contrast-target: calc( var(--current-semantic-target, var(--wcag3-body-normal)) * (1 + var(--weight-adjustment-factor)) );

    /* === APCA Dynamic Color Engine === */
    /* Calculate target lightness based on background and final target Lc */
    --compute-apca-color: calc( pow( abs( var(--parent-bg-l, 0.95) - pow( (var(--final-contrast-target, var(--wcag3-body-normal)) + var(--apca-offset)) / var(--apca-multiplier), 1 / var(--apca-exponent) ) ), 1 / var(--apca-exponent) ) * sign(var(--parent-bg-l, 0.95) - 0.5) );

    /* === Final Color Application === */
    /* Define final semantic colors using OKLCH */
    /* Clamp the calculated target lightness (--compute-apca-color) against the minimum required lightness (--*-text-min-l) */
    /* Ensures minimum readability while aiming for the calculated target contrast */
    --text-contrast: oklch( clamp(var(--body-text-min-l), var(--compute-apca-color), calc(var(--body-text-min-l) + 0.2)) var(--text-c, var(--rp-text-c)) var(--text-h, var(--rp-text-h)) / var(--text-alpha, 0.9) );
    --code-contrast: oklch( clamp(var(--code-text-min-l), var(--compute-apca-color), calc(var(--code-text-min-l) + 0.2)) var(--code-c, var(--rp-foam-c)) var(--code-h, var(--rp-foam-h)) / 1 );
    --heading-contrast: oklch( clamp(var(--heading-text-min-l), var(--compute-apca-color), calc(var(--heading-text-min-l) + 0.2)) var(--heading-c, var(--rp-iris-c)) var(--heading-h, var(--rp-iris-h)) / 1 );
    --blockquote-contrast: oklch( clamp(var(--blockquote-text-min-l), var(--compute-apca-color), calc(var(--blockquote-text-min-l) + 0.2)) var(--blockquote-c, var(--rp-muted-c)) var(--blockquote-h, var(--rp-muted-h)) / 0.8 ); /* Original 0.8 alpha */
    --ui-text-contrast: oklch( clamp(var(--ui-text-min-l), var(--compute-apca-color), calc(var(--ui-text-min-l) + 0.2)) var(--text-c, var(--rp-text-c)) var(--text-h, var(--rp-text-h)) / var(--text-alpha, 0.9) );

    /* === Adaptive Font Weight Calculation === */
    /* Normalize font size relative to 1rem for calculations */
    --font-size-normalized: calc(var(--optical-size) / 1rem);
    /* Calculate adaptive weights based on base weights and font size */
    --font-weight-sans-adaptive: clamp( 300, round( nearest, calc(var(--font-weight-base) * var(--font-size-normalized, 1) * 0.9), 100 ), 700 );
    --font-weight-serif-adaptive: clamp( 500, round( nearest, calc( var(--font-weight-heading) * var(--font-size-normalized, 1) * 1.1 ), 100 ), 800 );
    --font-weight-mono-adaptive: clamp( 300, round( nearest, calc(var(--font-weight-base) * var(--font-size-normalized, 1)), 100 ), 700 );

  } /* End :root inside @adaptations */

  /* === Background Propagation Listener === */
  /* Updates --parent-bg-l when an element gets an inline background */
  /* Calculations in this layer depending on --parent-bg-l will update automatically */
  *[style*="background"],
  *[style*="background-color"] {
    --parent-bg-l: var(--element-bg-l, var(--parent-bg-l));
  }

  /* === Element-Specific Context Setting === */
  /* Use :where for low specificity */
  /* Set context variables needed for calculations above */
  :where(body, p, li, div:not([class]), span:not([class]), td, th) {
    --element-font-size: var(--optical-size);
    --element-threshold-small: var(--wcag3-body-small);
    --element-threshold-normal: var(--wcag3-body-normal);
    --element-threshold-large: var(--wcag3-body-large);
    --current-semantic-target: var(--get-size-based-threshold);
    --current-font-family: var(--font-sans);
    --current-font-weight: var(--font-weight-sans-adaptive); /* Use adaptive weight */
    --current-line-height: var(--line-height-body);
    --current-letter-spacing: var(--letter-spacing-base);
    --text-alpha: 0.9; /* Reset alpha */
  }
  :where(h1, h2, h3, h4, h5, h6) {
    /* --element-font-size set individually below */
    --element-threshold-small: var(--wcag3-heading-small);
    --element-threshold-normal: var(--wcag3-heading-normal);
    --element-threshold-large: var(--wcag3-heading-large);
    --current-semantic-target: var(--get-size-based-threshold);
    --current-font-family: var(--font-serif);
    --current-font-weight: var(--font-weight-serif-adaptive); /* Use adaptive weight */
    --current-line-height: var(--line-height-heading);
    --current-letter-spacing: var(--letter-spacing-heading);
    --text-alpha: 1; /* Reset alpha */
    --heading-c: var(--rp-iris-c); --heading-h: var(--rp-iris-h); /* Reset color components */
  }
  :where(h1) { --element-font-size: var(--h1); }
  :where(h2) { --element-font-size: var(--h2); }
  :where(h3) { --element-font-size: var(--h3); }
  :where(h4) { --element-font-size: var(--h4); }
  :where(h5) { --element-font-size: var(--h5); }
  :where(h6) { --element-font-size: var(--h6); }

  :where(code, pre, kbd, samp) {
    --element-font-size: calc(var(--optical-size) * 0.9);
    --element-threshold-small: var(--wcag3-body-small);
    --element-threshold-normal: var(--wcag3-body-normal);
    --element-threshold-large: var(--wcag3-body-large);
    --current-semantic-target: var(--get-size-based-threshold);
    --current-font-family: var(--font-mono);
    --current-font-weight: var(--font-weight-mono-adaptive); /* Use adaptive weight */
    --current-line-height: var(--line-height-code);
    --current-letter-spacing: 0;
    --text-alpha: 1; /* Reset alpha */
    --code-c: var(--rp-foam-c); --code-h: var(--rp-foam-h); /* Reset color components */
  }
  :where(blockquote, blockquote *) {
    --element-font-size: var(--optical-size);
    --element-threshold-small: var(--wcag3-body-small);
    --element-threshold-normal: var(--wcag3-body-normal);
    --element-threshold-large: var(--wcag3-body-large);
    /* Reduce target Lc for blockquotes */
    --current-semantic-target: calc(var(--get-size-based-threshold) * 0.9);
    --current-font-family: var(--font-sans);
    --current-font-weight: var(--font-weight-sans-adaptive); /* Use adaptive weight */
    --current-line-height: calc(var(--line-height-body) * 0.95);
    --current-letter-spacing: -0.01em;
    --text-alpha: 0.8; /* Set blockquote alpha */
    --blockquote-c: var(--rp-muted-c); --blockquote-h: var(--rp-muted-h); /* Reset color components */
  }
  :where(button, a, label, input, select, textarea) {
    --element-font-size: var(--optical-size);
    --element-threshold-small: var(--wcag3-ui-small);
    --element-threshold-normal: var(--wcag3-ui-normal);
    --element-threshold-large: var(--wcag3-ui-large);
    --current-semantic-target: var(--get-size-based-threshold);
    /* Inherit font family/weight unless specified */
    --text-alpha: 1; /* Reset alpha */
    --text-c: var(--rp-text-c); --text-h: var(--rp-text-h); /* Reset color components */
  }

  /* Disabled/Placeholder/Invalid Context */
  :where(:disabled) { --current-semantic-target: var(--wcag3-disabled-text); opacity: 1; }
  :where(::placeholder) { --current-semantic-target: var(--wcag3-placeholder); }
  :where(:invalid) {
    --text-c: var(--rp-love-c); /* Use Love color for invalid */
    --text-h: var(--rp-love-h);
    --current-semantic-target: 90; /* High contrast target Lc for invalid state */
  }

  /* === Apply Dynamic Colors === */
  /* Apply the final calculated contrast colors */
  body, p, li, div:not([class]), span:not([class]), td, th { color: var(--text-contrast); }
  h1, h2, h3, h4, h5, h6 { color: var(--heading-contrast); }
  code, pre, kbd, samp { color: var(--code-contrast); }
  blockquote, blockquote * { color: var(--blockquote-contrast); }
  button, a, label, input, select, textarea { color: var(--ui-text-contrast); }
  svg:not([aria-hidden="true"]) { color: var(--ui-text-contrast); } /* Example SVG */

  /* Apply backgrounds calculated here */
  pre { background-color: oklch(var(--code-block-bg-l) 0.03 240); }
  code:not(pre code) { background: oklch(var(--code-bg-l) 0.05 290 / 0.15); }

} /* End @layer adaptations */