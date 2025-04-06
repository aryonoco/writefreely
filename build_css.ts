#!/usr/bin/env bun

import { dirname } from "path";
import { mkdir } from "fs/promises";
import { performance } from "node:perf_hooks";

declare global {
  interface ImportMeta {
    readonly main: boolean;
  }
}

type CssLayerName =
  | "reset"
  | "base"
  | "typography"
  | "colors"
  | "components"
  | "utilities";
type CssLayerPath = `css/layers/${CssLayerName}.css`;

interface CssLayer {
  readonly name: CssLayerName;
  readonly path: CssLayerPath;
}

interface LayerContent {
  name: CssLayerName;
  content: string;
}

const { $, file, write }: typeof Bun = Bun;
const outputPath = "dist/css/style.min.css" as const;

const layers = [
  { name: "reset", path: "css/layers/reset.css" },
  { name: "base", path: "css/layers/base.css" },
  { name: "typography", path: "css/layers/typography.css" },
  { name: "colors", path: "css/layers/colors.css" },
  { name: "components", path: "css/layers/components.css" },
  { name: "utilities", path: "css/layers/utilities.css" },
] as const satisfies readonly CssLayer[];

const dependencies = [
  "postcss",
  "postcss-minify",
  "css-mqpacker",
  "cssnano",
] as const;

async function ensureDependencies() {
  for (const dep of dependencies) {
    const depPath = `node_modules/${dep}/package.json`;
    if (!(await file(depPath).exists())) {
      console.log(`📦 Installing latest ${dep}...`);
      await $`bun add ${dep} --no-save`.quiet();
    }
  }
}

async function buildCSS(
  postcss: { default: typeof import("postcss").default },
  plugins: import("postcss").AcceptedPlugin[],
) {
  const start = performance.now();

  try {
    await mkdir(dirname(outputPath), { recursive: true });

    const [originalSize, layerContents] = await Promise.all([
      Promise.all(layers.map(async ({ path }) => file(path).size)).then((s) =>
        s.reduce((a: number, b: number) => a + b, 0),
      ),
      Promise.all(
        layers.map(async ({ path, name }) => ({
          name,
          content: await file(path).text(),
        })),
      ),
    ]);

    const combinedCSS = `
          @layer ${layers.map((l) => l.name).join(", ")};
          ${layerContents
            .map(
              ({ name, content }: { name: CssLayerName; content: string }) =>
                `@layer ${name} {\n${content}\n}`,
            )
            .join("\n\n")}
        `;

    const processed = await postcss.default(plugins).process(combinedCSS, {
      from: "virtual:combined.css",
      to: outputPath,
      map: { inline: false },
    });

    await write(outputPath, processed.css);
    if (processed.map) {
      await write(`${outputPath}.map`, JSON.stringify(processed.map));
    }

    const gzip = await $`gzip -c ${outputPath}`.arrayBuffer();
    const gzipSize = gzip.byteLength;

    return {
      timeTaken: performance.now() - start,
      originalSize,
      finalSize: file(outputPath).size,
      reduction: Number(
        (((originalSize - gzipSize) / originalSize) * 100).toFixed(2),
      ),
      gzipSize,
    };
  } catch (error) {
    console.error("Build failed:", error);
    process.exit(1);
  }
}

async function main() {
  await ensureDependencies();
  const [
    { default: postcss },
    cssnanoModule, // Import the module
    { default: mqpacker },
    { default: postcssImport },
  ] = await Promise.all([
    import("postcss"),
    import("cssnano"),
    import("css-mqpacker"),
    import("postcss-import"),
  ]);
  const cssnanoPlugin = cssnanoModule.default;
  const result = await buildCSS(postcss, [
    postcssImport(),
    mqpacker({ sort: true }),
    cssnanoPlugin({
      preset: [
        "default",
        {
          discardComments: { removeAll: true },
          discardDuplicates: true,
          discardEmpty: true,
          normalizeWhitespace: true,
          normalizeUnicode: true,
          calc: false,
          colormin: false,
          convertValues: false,
          minifyFontValues: false,
          minifyGradients: false,
          mergeIdents: false,
          mergeLonghand: false,
          mergeRules: false,
          minifyParams: false,
          minifySelectors: false,
          normalizePositions: false,
          normalizeUrl: false,
          discardUnused: false,
        },
      ],
    }),
  ]);

  console.log(`✅ CSS built with size reduction of ${result.reduction}%`);
  console.log(`   Original: ${result.originalSize} bytes`);
  console.log(`   Minified: ${result.finalSize} bytes`);
  console.log(`   Gzip: ${result.gzipSize} bytes`);
  console.log(`   Time: ${result.timeTaken.toFixed(2)}ms`);
}

if (import.meta.main) {
  await main().catch((error) => {
    console.error(
      "❌ Build failed:",
      error instanceof Error ? error.message : error,
    );
    process.exit(1);
  });
}
