import * as vscode from 'vscode';
import {
  ExecuteCommandRequest,
  LanguageClient,
  LanguageClientOptions,
  ServerOptions,
  TransportKind,
} from 'vscode-languageclient/node';
import { resolveServer } from './server';

const jsxLanguages = ['typescriptreact', 'javascriptreact', 'typescript', 'javascript'];
const styleLanguages = ['scss', 'css', 'sass'];

let client: LanguageClient | undefined;
let output: vscode.LogOutputChannel | undefined;

export async function activate(context: vscode.ExtensionContext): Promise<void> {
  output = vscode.window.createOutputChannel('BEM', { log: true });
  context.subscriptions.push(output);

  context.subscriptions.push(
    vscode.commands.registerCommand('bem.restartServer', async () => {
      if (client) {
        await client.restart();
      } else {
        await start(context);
      }
    }),
    vscode.commands.registerCommand('bem.showSkeleton', () => showSkeleton()),
    vscode.workspace.onDidChangeConfiguration(async (e) => {
      if (e.affectsConfiguration('bem.server.path')) {
        const choice = await vscode.window.showInformationMessage(
          'BEM: the server path changed. Restart the server?',
          'Restart',
        );
        if (choice === 'Restart') {
          await stop();
          await start(context);
        }
      }
    }),
  );
  await start(context);
}

export async function deactivate(): Promise<void> {
  await stop();
}

async function stop(): Promise<void> {
  const c = client;
  client = undefined;
  if (c) {
    await c.stop();
  }
}

/** The "bem" settings as plain JSON, for initializationOptions. */
function settings(): unknown {
  return JSON.parse(JSON.stringify(vscode.workspace.getConfiguration('bem')));
}

async function start(context: vscode.ExtensionContext): Promise<void> {
  let resolved;
  try {
    resolved = resolveServer({
      configured: vscode.workspace.getConfiguration('bem').get<string>('server.path'),
      extensionPath: context.extensionPath,
      workspaceFolder: vscode.workspace.workspaceFolders?.[0]?.uri.fsPath,
    });
  } catch (err) {
    vscode.window.showErrorMessage(`BEM: ${(err as Error).message}`);
    return;
  }
  if (!resolved) {
    vscode.window.showErrorMessage(
      'BEM: no bem-ls binary found. Install the platform-specific extension, put bem-ls on your PATH, or set bem.server.path.',
    );
    return;
  }
  output?.info(`Starting ${resolved.command} (${resolved.source})`);

  const serverOptions: ServerOptions = {
    run: { command: resolved.command, args: ['--stdio'], transport: TransportKind.stdio },
    debug: { command: resolved.command, args: ['--stdio'], transport: TransportKind.stdio },
  };
  const documentSelector = [...jsxLanguages, ...styleLanguages].map((language) => ({ scheme: 'file', language }));
  const clientOptions: LanguageClientOptions = {
    documentSelector,
    outputChannel: output,
    initializationOptions: settings(),
    synchronize: {
      configurationSection: 'bem',
      fileEvents: vscode.workspace.createFileSystemWatcher('**/{*.scss,*.css,*.sass,*.tsx,*.jsx,*.ts,*.js,bem.config.json}'),
    },
  };

  client = new LanguageClient('bem', 'BEM', serverOptions, clientOptions);
  await client.start();

  // vscode-languageclient 10 handles textDocument/inlineCompletion itself.
  // With an older client library, forward ghost-text requests by hand.
  if (!client.getFeature('textDocument/inlineCompletion') && client.initializeResult?.capabilities.inlineCompletionProvider) {
    output?.info('Using the fallback inline completion provider');
    context.subscriptions.push(registerInlineFallback(client));
  }
}

function registerInlineFallback(c: LanguageClient): vscode.Disposable {
  const selector = styleLanguages.map((language) => ({ scheme: 'file', language }));
  return vscode.languages.registerInlineCompletionItemProvider(selector, {
    async provideInlineCompletionItems(doc, position, ctx, token) {
      const result = await c.sendRequest<{ items: { insertText: string; range?: unknown }[] } | null>(
        'textDocument/inlineCompletion',
        {
          textDocument: c.code2ProtocolConverter.asTextDocumentIdentifier(doc),
          position: c.code2ProtocolConverter.asPosition(position),
          context: { triggerKind: ctx.triggerKind === vscode.InlineCompletionTriggerKind.Invoke ? 1 : 2 },
        },
        token,
      );
      return (result?.items ?? []).map(
        (it) =>
          new vscode.InlineCompletionItem(
            it.insertText,
            it.range ? c.protocol2CodeConverter.asRange(it.range as never) : undefined,
          ),
      );
    },
  });
}

async function showSkeleton(): Promise<void> {
  const editor = vscode.window.activeTextEditor;
  if (!editor || !jsxLanguages.includes(editor.document.languageId)) {
    vscode.window.showWarningMessage('BEM: open a JSX/TSX component first.');
    return;
  }
  if (!client) {
    vscode.window.showErrorMessage('BEM: the server is not running.');
    return;
  }
  const scss = await client.sendRequest(ExecuteCommandRequest.type, {
    command: 'bem.generateSkeleton',
    arguments: [editor.document.uri.toString()],
  });
  if (typeof scss !== 'string' || scss === '') {
    vscode.window.showInformationMessage('BEM: no BEM classes found in this component.');
    return;
  }
  const doc = await vscode.workspace.openTextDocument({ language: 'scss', content: scss });
  await vscode.window.showTextDocument(doc, { viewColumn: vscode.ViewColumn.Beside, preview: true });
}
