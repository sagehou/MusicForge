// Actions-only: reject UI fast paths unless executable TSX syntax is unchanged.
import { createRequire } from "node:module";
import { execFileSync } from "node:child_process";
import { appendFileSync, readFileSync, writeFileSync } from "node:fs";
import { pathToFileURL } from "node:url";

const ts = createRequire(new URL("../../web/package.json", import.meta.url))("typescript");

export function presentationSignature(source) {
  const file = ts.createSourceFile("ui.tsx", source, ts.ScriptTarget.Latest, true, ts.ScriptKind.TSX);
  if (file.parseDiagnostics.length) throw new Error("Invalid TSX");
  const literal = node => ts.isStringLiteral(node) || ts.isNumericLiteral(node)
    || ts.isPrefixUnaryExpression(node) && node.operator === ts.SyntaxKind.MinusToken && ts.isNumericLiteral(node.operand);
  const transformed = ts.transform(file, [context => {
    function visit(node) {
      if (ts.isJsxText(node)) return ts.factory.createJsxText("presentation");
      if (ts.isJsxAttribute(node)) {
        const name = node.name.getText(file);
        const value = node.initializer;
        const staticText = value && (ts.isStringLiteral(value) || ts.isJsxExpression(value) && value.expression && ts.isStringLiteral(value.expression));
        const staticStyle = name === "style" && value && ts.isJsxExpression(value) && value.expression
          && ts.isObjectLiteralExpression(value.expression) && value.expression.properties.every(property =>
            ts.isPropertyAssignment(property) && !ts.isComputedPropertyName(property.name) && literal(property.initializer));
        if (["className", "title", "aria-label", "placeholder"].includes(name) && staticText || staticStyle) {
          return ts.factory.updateJsxAttribute(node, node.name, ts.factory.createStringLiteral("presentation"));
        }
      }
      return ts.visitEachChild(node, visit, context);
    }
    return root => ts.visitNode(root, visit);
  }]);
  try { return ts.createPrinter().printFile(transformed.transformed[0]); }
  finally { transformed.dispose(); }
}

export function presentationOnly(before, after) {
  return presentationSignature(before) === presentationSignature(after);
}

function main() {
  const plan = JSON.parse(readFileSync(".ci/ci-scope.json", "utf8"));
  const paths = execFileSync("git", ["diff", "--no-renames", "--name-only", "-z", plan.acceptance_base, "HEAD", "--"], { encoding: "utf8" }).split("\0").filter(Boolean);
  const uncertain = paths.filter(path => path.endsWith(".tsx") || path.endsWith(".svg")).filter(path => {
    // SVG can contain active content; keep it on full acceptance for now.
    if (path.endsWith(".svg")) return true;
    try {
      const before = execFileSync("git", ["show", `${plan.acceptance_base}:${path}`], { encoding: "utf8", stdio: ["ignore", "pipe", "pipe"] });
      return !presentationOnly(before, readFileSync(path, "utf8"));
    } catch { return true; }
  });
  const scope = uncertain.length ? "full" : "ui";
  const specs = new Set(["appearance.spec.ts", "workspace-layout.spec.ts"]);
  const global = paths.some(path => path.endsWith(".css") || path.includes("/locales/") || ["web/src/App.tsx", "web/src/i18n.tsx", "web/src/main.tsx"].includes(path) || path.includes("/ui/") || /\/(resource|page-heading|detail-panel|theme-selector)\.tsx$/.test(path));
  const affected = {
    "library": ["library-experience", "lyrics", "library-comparison"], "lyrics": ["lyrics"],
    "repair": ["repair"], "overview": ["cleanup-and-ui"],
  };
  if (global) for (const name of ["library-experience", "ui-refinement", "cleanup-and-ui", "bulk-jobs", "repair", "lyrics", "library-comparison", "session-recovery"]) specs.add(`${name}.spec.ts`);
  else for (const [part, names] of Object.entries(affected)) if (paths.some(path => path.includes(part))) for (const name of names) specs.add(`${name}.spec.ts`);
  writeFileSync(".ci/ui-specs.txt", [...specs].join("\n") + "\n");
  plan.scope = scope;
  plan.uncertain_ui_files = uncertain;
  plan.browser_specs = [...specs];
  writeFileSync(".ci/ci-scope.json", JSON.stringify(plan, null, 2) + "\n");
  appendFileSync(process.env.GITHUB_OUTPUT, `scope=${scope}\n`);
  appendFileSync(process.env.GITHUB_STEP_SUMMARY, uncertain.length
    ? `\nTSX/static-content gate: **full acceptance**; executable or uncertain changes: ${uncertain.map(path => `\`${path}\``).join(", ")}.\n`
    : `\nTSX gate: **presentation only**. UI browser checks: ${[...specs].join(", ")}.\n`);
}

if (process.argv[1] && import.meta.url === pathToFileURL(process.argv[1]).href) main();
