import * as vscode from 'vscode';
import { execFile } from 'child_process';
import { promisify } from 'util';
import {
  LanguageClient,
  LanguageClientOptions,
  ServerOptions,
  TransportKind,
} from 'vscode-languageclient/node';

const execFileAsync = promisify(execFile);

let client: LanguageClient | undefined;
let statusBarItem: vscode.StatusBarItem;
let outputChannel: vscode.OutputChannel;

interface KatesTemplateItem {
  id: string;
  name: string;
  dialect: string;
  description: string;
  content: string;
}

interface KatesPreflightResult {
  dialect: string;
  connected: boolean;
  brokerCount: number;
  topicCount: number;
  offlineErrors: number;
  warnings?: string[];
  dryRun?: {
    wouldSucceed: boolean;
    totalBrokers: number;
    errors?: string[];
    warnings?: string[];
    steps?: Array<{
      name: string;
      disruptionType: string;
      targetPod?: string;
      resolvedLeaderId?: number;
      affectedPods?: string[];
      warnings?: string[];
    }>;
  };
}

export async function activate(context: vscode.ExtensionContext): Promise<void> {
  const config = vscode.workspace.getConfiguration('kates');
  const katesPath = config.get<string>('executablePath', 'kates');

  outputChannel = vscode.window.createOutputChannel('Kates YAML Studio');
  context.subscriptions.push(outputChannel);

  const serverOptions: ServerOptions = {
    command: katesPath,
    args: ['lsp', '--stdio'],
    transport: TransportKind.stdio,
  };

  const clientOptions: LanguageClientOptions = {
    documentSelector: [
      { scheme: 'file', language: 'yaml' },
      { scheme: 'file', language: 'json' },
      { scheme: 'untitled', language: 'yaml' },
    ],
    synchronize: {
      fileEvents: vscode.workspace.createFileSystemWatcher('**/*.{yaml,yml,json}'),
    },
  };

  client = new LanguageClient(
    'kates-lsp',
    'Kates YAML Language Server',
    serverOptions,
    clientOptions
  );

  statusBarItem = vscode.window.createStatusBarItem(vscode.StatusBarAlignment.Right, 100);
  statusBarItem.command = 'kates.switchDialect';
  context.subscriptions.push(statusBarItem);

  context.subscriptions.push(
    vscode.window.onDidChangeActiveTextEditor((editor) => {
      void updateStatusBar(editor);
    }),
    vscode.workspace.onDidSaveTextDocument(() => {
      void updateStatusBar(vscode.window.activeTextEditor);
    })
  );

  registerCommands(context, katesPath);

  await client.start();
  await updateStatusBar(vscode.window.activeTextEditor);

  if (config.get<boolean>('enableTestExplorer', true)) {
    registerTestExplorer(context, katesPath);
  }
}

async function updateStatusBar(editor: vscode.TextEditor | undefined): Promise<void> {
  if (!editor || (editor.document.languageId !== 'yaml' && editor.document.languageId !== 'json')) {
    statusBarItem.hide();
    return;
  }
  if (!client || !client.isRunning()) {
    statusBarItem.hide();
    return;
  }
  try {
    const res = await client.sendRequest<{ dialect: string; schema: string }>('kates/detectDialect', {
      uri: editor.document.uri.toString(),
    });
    if (res && res.dialect) {
      statusBarItem.text = `$(symbol-namespace) Kates: ${res.dialect}`;
      statusBarItem.tooltip = `Validated against ${res.schema} (click to change # kates-schema directive)`;
      statusBarItem.show();
    } else {
      statusBarItem.text = `$(symbol-namespace) Kates YAML`;
      statusBarItem.tooltip = `Click to set # kates-schema dialect directive`;
      statusBarItem.show();
    }
  } catch {
    statusBarItem.hide();
  }
}

function resolveTargetFilePath(uriArg?: string): string | undefined {
  if (uriArg) {
    return vscode.Uri.parse(uriArg).fsPath;
  }
  return vscode.window.activeTextEditor?.document.uri.fsPath;
}

function runInKatesTerminal(title: string, cmdLine: string): void {
  const term =
    vscode.window.terminals.find((t) => t.name === title) ??
    vscode.window.createTerminal(title);
  term.show(true);
  term.sendText(cmdLine);
}

function lintSubcommandForDialect(dialect?: string): string[] {
  switch (dialect) {
    case 'flow':
      return ['flow', 'lint'];
    case 'resilience':
      return ['resilience', 'lint'];
    case 'disruption':
      return ['disruption', 'lint'];
    case 'compliance':
      return ['compliance', 'lint'];
    default:
      return ['test', 'lint'];
  }
}

function registerCommands(context: vscode.ExtensionContext, katesBin: string): void {
  context.subscriptions.push(
    vscode.commands.registerCommand('kates.runScenario', (uri?: string) => {
      const file = resolveTargetFilePath(uri);
      if (file) {
        runInKatesTerminal('Kates Test', `${katesBin} test apply -f "${file}" --wait`);
      }
    }),
    vscode.commands.registerCommand('kates.runFlow', (uri?: string) => {
      const file = resolveTargetFilePath(uri);
      if (file) {
        runInKatesTerminal('Kates Flow', `${katesBin} flow run -f "${file}"`);
      }
    }),
    vscode.commands.registerCommand('kates.runResilience', (uri?: string) => {
      const file = resolveTargetFilePath(uri);
      if (file) {
        runInKatesTerminal('Kates Chaos', `${katesBin} resilience run -f "${file}"`);
      }
    }),
    vscode.commands.registerCommand('kates.dryRunDisruption', (uri?: string) => {
      const file = resolveTargetFilePath(uri);
      if (file) {
        runInKatesTerminal('Kates Chaos', `${katesBin} disruption run --config "${file}" --dry-run`);
      }
    }),
    vscode.commands.registerCommand('kates.runDisruption', (uri?: string) => {
      const file = resolveTargetFilePath(uri);
      if (file) {
        runInKatesTerminal('Kates Chaos', `${katesBin} disruption run --config "${file}"`);
      }
    }),
    vscode.commands.registerCommand('kates.checkCompliance', (uri?: string) => {
      const file = resolveTargetFilePath(uri);
      if (file) {
        runInKatesTerminal('Kates Compliance', `${katesBin} compliance check -f "${file}"`);
      }
    }),
    vscode.commands.registerCommand('kates.formatCompliance', (uri?: string) => {
      const file = resolveTargetFilePath(uri);
      if (file) {
        runInKatesTerminal('Kates Compliance', `${katesBin} compliance fmt -w "${file}"`);
      }
    }),
    vscode.commands.registerCommand('kates.lintFile', (uri?: string, dialect?: string) => {
      const file = resolveTargetFilePath(uri);
      if (!file) {
        return;
      }
      const subCmd = lintSubcommandForDialect(dialect).join(' ');
      runInKatesTerminal('Kates Lint', `${katesBin} ${subCmd} "${file}"`);
    }),
    vscode.commands.registerCommand('kates.switchDialect', async () => {
      const editor = vscode.window.activeTextEditor;
      if (!editor) {
        return;
      }
      const picks = [
        { label: 'scenario', description: 'CLI Scenario File (scenarios: / kates test apply)' },
        { label: 'test-request', description: 'Multi-Phase CreateTestRequest / TestScenario' },
        { label: 'flow', description: 'Flow Pipeline (steps: / kates flow run)' },
        { label: 'resilience', description: 'Resilience Chaos Test (testRequest + chaosSpec + probes)' },
        { label: 'disruption', description: 'Multi-Step Disruption Plan / Playbook' },
        { label: 'compliance', description: 'KafkaReference Compliance Architecture (conformity.kates.io/v1alpha1)' },
      ];
      const chosen = await vscode.window.showQuickPick(picks, {
        placeHolder: 'Select Kates YAML dialect for this document',
      });
      if (!chosen) {
        return;
      }
      const firstLine = editor.document.lineAt(0);
      const directive = `# kates-schema: ${chosen.label}`;
      await editor.edit((editBuilder) => {
        if (firstLine.text.startsWith('# kates-schema:')) {
          editBuilder.replace(firstLine.range, directive);
        } else {
          editBuilder.insert(new vscode.Position(0, 0), directive + '\n');
        }
      });
      await updateStatusBar(editor);
    }),
    vscode.commands.registerCommand('kates.newFromTemplate', async () => {
      if (!client || !client.isRunning()) {
        void vscode.window.showWarningMessage('Kates Language Server is not running.');
        return;
      }
      const templates = await client.sendRequest<KatesTemplateItem[]>('kates/listTemplates', {});
      if (!templates || templates.length === 0) {
        return;
      }
      const chosen = await vscode.window.showQuickPick(
        templates.map((t) => ({
          label: `$(file-code) ${t.name}`,
          description: `[${t.dialect}]`,
          detail: t.description,
          template: t,
        })),
        { placeHolder: 'Choose a Kates YAML template to scaffold (Scenario, Multi-Phase, Chaos, Flow, or Compliance)' }
      );
      if (!chosen) {
        return;
      }
      const doc = await vscode.workspace.openTextDocument({
        language: 'yaml',
        content: chosen.template.content,
      });
      await vscode.window.showTextDocument(doc);
    }),
    vscode.commands.registerCommand('kates.diffScenarios', async (uri?: string) => {
      if (!client || !client.isRunning()) {
        return;
      }
      const leftDoc = uri
        ? await vscode.workspace.openTextDocument(vscode.Uri.parse(uri))
        : vscode.window.activeTextEditor?.document;
      if (!leftDoc) {
        void vscode.window.showInformationMessage('Open a Kates scenario YAML file first to compare it.');
        return;
      }
      const rightUris = await vscode.window.showOpenDialog({
        canSelectMany: false,
        openLabel: 'Compare Scenario',
        filters: { 'YAML / JSON Scenario': ['yaml', 'yml', 'json'] },
      });
      if (!rightUris || rightUris.length === 0) {
        return;
      }
      const rightDoc = await vscode.workspace.openTextDocument(rightUris[0]);
      const res = await client.sendRequest<{ markdown?: string; error?: string }>('kates/scenarioDiff', {
        leftUri: leftDoc.uri.toString(),
        leftText: leftDoc.getText(),
        rightUri: rightDoc.uri.toString(),
        rightText: rightDoc.getText(),
      });
      if (res.error || !res.markdown) {
        void vscode.window.showErrorMessage(res.error ?? 'Failed to compare scenarios.');
        return;
      }
      const mdDoc = await vscode.workspace.openTextDocument({
        language: 'markdown',
        content: res.markdown,
      });
      await vscode.commands.executeCommand('markdown.showPreviewToSide', mdDoc.uri);
    }),
    vscode.commands.registerCommand('kates.clusterPreflight', async (uri?: string) => {
      if (!client || !client.isRunning()) {
        return;
      }
      const doc = uri
        ? await vscode.workspace.openTextDocument(vscode.Uri.parse(uri))
        : vscode.window.activeTextEditor?.document;
      if (!doc) {
        return;
      }
      const res = await client.sendRequest<KatesPreflightResult>('kates/clusterPreflight', {
        uri: doc.uri.toString(),
        text: doc.getText(),
      });
      outputChannel.clear();
      outputChannel.appendLine('═══════════════════════════════════════════════════════════════');
      outputChannel.appendLine(`  Kates Live Cluster Preflight (${res.dialect || 'unknown'})`);
      outputChannel.appendLine('═══════════════════════════════════════════════════════════════');
      outputChannel.appendLine(`  Cluster Connected : ${res.connected ? 'YES' : 'NO (offline mode)'}`);
      if (res.connected) {
        outputChannel.appendLine(`  Active Brokers    : ${res.brokerCount}`);
        outputChannel.appendLine(`  Visible Topics    : ${res.topicCount}`);
      }
      outputChannel.appendLine(`  Offline Lint Errs : ${res.offlineErrors}`);
      if (res.warnings && res.warnings.length > 0) {
        outputChannel.appendLine('\n  Warnings:');
        for (const w of res.warnings) {
          outputChannel.appendLine(`    ⚠ ${w}`);
        }
      }
      if (res.dryRun && res.dryRun.steps) {
        outputChannel.appendLine(`\n  Disruption Dry-Run Verdict: ${res.dryRun.wouldSucceed ? 'SAFE' : 'UNSAFE'}`);
        for (const step of res.dryRun.steps) {
          outputChannel.appendLine(`    • Step "${step.name}" (${step.disruptionType})`);
          if (step.targetPod) {
            outputChannel.appendLine(`      Target Pod      : ${step.targetPod}`);
          }
          if (step.resolvedLeaderId !== undefined) {
            outputChannel.appendLine(`      Resolved Leader : broker-${step.resolvedLeaderId}`);
          }
          if (step.affectedPods && step.affectedPods.length > 0) {
            outputChannel.appendLine(`      Affected Pods   : ${step.affectedPods.join(', ')}`);
          }
        }
      }
      outputChannel.show(true);
    })
  );
}

function registerTestExplorer(context: vscode.ExtensionContext, katesBin: string): void {
  const controller = vscode.tests.createTestController(
    'katesTestController',
    'Kates YAML Tests & Compliance'
  );
  context.subscriptions.push(controller);

  const fileDialectMap = new Map<string, string>();

  const discoverFile = async (uri: vscode.Uri): Promise<void> => {
    if (!client || !client.isRunning()) {
      return;
    }
    try {
      const doc = await vscode.workspace.openTextDocument(uri);
      const det = await client.sendRequest<{ dialect: string }>('kates/detectDialect', {
        uri: uri.toString(),
      });
      if (!det || !det.dialect) {
        controller.items.delete(uri.toString());
        return;
      }
      fileDialectMap.set(uri.toString(), det.dialect);
      const fileItem = controller.createTestItem(
        uri.toString(),
        `${vscode.workspace.asRelativePath(uri)} [${det.dialect}]`,
        uri
      );
      const symbols = await client.sendRequest<
        Array<{
          name: string;
          detail?: string;
          range: { start: { line: number; character: number }; end: { line: number; character: number } };
        }>
      >('textDocument/documentSymbol', {
        textDocument: { uri: uri.toString() },
      });
      if (Array.isArray(symbols)) {
        for (const sym of symbols) {
          const child = controller.createTestItem(
            `${uri.toString()}#${sym.name}`,
            sym.detail ? `${sym.name} (${sym.detail})` : sym.name,
            uri
          );
          child.range = new vscode.Range(
            new vscode.Position(sym.range.start.line, sym.range.start.character),
            new vscode.Position(sym.range.end.line, sym.range.end.character)
          );
          fileItem.children.add(child);
        }
      }
      fileDialectMap.set(fileItem.id, det.dialect);
      controller.items.add(fileItem);
      void doc;
    } catch {
      // Ignore non-Kates files
    }
  };

  controller.resolveHandler = async (item) => {
    if (!item) {
      const uris = await vscode.workspace.findFiles(
        '**/*.{yaml,yml}',
        '{**/node_modules/**,**/.git/**,**/charts/**,**/helm/**}',
        250
      );
      for (const u of uris) {
        await discoverFile(u);
      }
    } else if (item.uri) {
      await discoverFile(item.uri);
    }
  };

  const watcher = vscode.workspace.createFileSystemWatcher('**/*.{yaml,yml}');
  watcher.onDidCreate((u) => void discoverFile(u));
  watcher.onDidChange((u) => void discoverFile(u));
  watcher.onDidDelete((u) => controller.items.delete(u.toString()));
  context.subscriptions.push(watcher);

  // Profile 1: Offline Lint & Validate (Default Run Profile)
  controller.createRunProfile(
    'Kates: Offline Lint & Validate',
    vscode.TestRunProfileKind.Run,
    async (request, token) => {
      const run = controller.createTestRun(request);
      const queue: vscode.TestItem[] = [];
      if (request.include) {
        request.include.forEach((t) => queue.push(t));
      } else {
        controller.items.forEach((t) => queue.push(t));
      }

      for (const item of queue) {
        if (token.isCancellationRequested) {
          break;
        }
        const targetUri = item.uri;
        if (!targetUri) {
          continue;
        }
        run.started(item);
        const startMs = Date.now();
        const dialect = fileDialectMap.get(targetUri.toString()) ?? 'scenario';
        const args = [...lintSubcommandForDialect(dialect), '-o', 'json', targetUri.fsPath];
        try {
          await execFileAsync(katesBin, args);
          run.passed(item, Date.now() - startMs);
          item.children.forEach((ch) => run.passed(ch, Date.now() - startMs));
        } catch (err: unknown) {
          const stdout = (err as { stdout?: string })?.stdout ?? '';
          try {
            const findings = JSON.parse(stdout) as Array<{
              line: number;
              col: number;
              severity: string;
              code: string;
              message: string;
            }>;
            const errors = findings.filter((f) => f.severity === 'error');
            if (errors.length === 0) {
              run.passed(item, Date.now() - startMs);
            } else {
              const msgs = errors.map((e) => {
                const m = new vscode.TestMessage(`[${e.code}] ${e.message}`);
                const line = Math.max((e.line || 1) - 1, 0);
                const col = Math.max((e.col || 1) - 1, 0);
                m.location = new vscode.Location(targetUri, new vscode.Position(line, col));
                return m;
              });
              run.failed(item, msgs, Date.now() - startMs);
            }
          } catch {
            run.failed(item, new vscode.TestMessage(String(err)), Date.now() - startMs);
          }
        }
      }
      run.end();
    },
    true
  );

  // Profile 2: Cluster Execution Profile
  controller.createRunProfile(
    'Kates: Execute on Cluster',
    vscode.TestRunProfileKind.Run,
    async (request) => {
      const items: vscode.TestItem[] = [];
      if (request.include) {
        request.include.forEach((i) => items.push(i));
      } else {
        controller.items.forEach((i) => items.push(i));
      }
      for (const item of items) {
        if (!item.uri) {
          continue;
        }
        const dialect = fileDialectMap.get(item.uri.toString()) ?? 'scenario';
        const uriStr = item.uri.toString();
        if (dialect === 'flow') {
          await vscode.commands.executeCommand('kates.runFlow', uriStr);
        } else if (dialect === 'resilience') {
          await vscode.commands.executeCommand('kates.runResilience', uriStr);
        } else if (dialect === 'disruption') {
          await vscode.commands.executeCommand('kates.dryRunDisruption', uriStr);
        } else if (dialect === 'compliance') {
          await vscode.commands.executeCommand('kates.checkCompliance', uriStr);
        } else {
          await vscode.commands.executeCommand('kates.runScenario', uriStr);
        }
      }
    },
    false
  );
}

export async function deactivate(): Promise<void> {
  if (client) {
    await client.stop();
  }
}
