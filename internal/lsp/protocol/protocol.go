// Package protocol holds the subset of Language Server Protocol types this
// server uses, written by hand from the 3.17 specification plus the 3.18
// inline completion types.
package protocol

import "encoding/json"

// DocumentURI is a file URI.
type DocumentURI string

// Position is zero-based. Character counts code units of the negotiated
// position encoding (UTF-16 by default).
type Position struct {
	Line      uint32 `json:"line"`
	Character uint32 `json:"character"`
}

// Range is half-open.
type Range struct {
	Start Position `json:"start"`
	End   Position `json:"end"`
}

// Location is a range in a document.
type Location struct {
	URI   DocumentURI `json:"uri"`
	Range Range       `json:"range"`
}

// LocationLink is a richer definition result.
type LocationLink struct {
	OriginSelectionRange *Range      `json:"originSelectionRange,omitempty"`
	TargetURI            DocumentURI `json:"targetUri"`
	TargetRange          Range       `json:"targetRange"`
	TargetSelectionRange Range       `json:"targetSelectionRange"`
}

// Position encodings.
const (
	EncodingUTF8  = "utf-8"
	EncodingUTF16 = "utf-16"
	EncodingUTF32 = "utf-32"
)

// ---------------------------------------------------------------------------
// Lifecycle

type InitializeParams struct {
	ProcessID             *int               `json:"processId"`
	RootURI               DocumentURI        `json:"rootUri,omitempty"`
	RootPath              string             `json:"rootPath,omitempty"`
	WorkspaceFolders      []WorkspaceFolder  `json:"workspaceFolders,omitempty"`
	InitializationOptions json.RawMessage    `json:"initializationOptions,omitempty"`
	Capabilities          ClientCapabilities `json:"capabilities"`
	ClientInfo            *ClientInfo        `json:"clientInfo,omitempty"`
}

type ClientInfo struct {
	Name    string `json:"name"`
	Version string `json:"version,omitempty"`
}

type WorkspaceFolder struct {
	URI  DocumentURI `json:"uri"`
	Name string      `json:"name"`
}

// ClientCapabilities lists only what the server checks.
type ClientCapabilities struct {
	General *struct {
		PositionEncodings []string `json:"positionEncodings,omitempty"`
	} `json:"general,omitempty"`
	Workspace *struct {
		WorkspaceEdit *struct {
			DocumentChanges    bool     `json:"documentChanges,omitempty"`
			ResourceOperations []string `json:"resourceOperations,omitempty"`
		} `json:"workspaceEdit,omitempty"`
		Configuration         bool `json:"configuration,omitempty"`
		DidChangeWatchedFiles *struct {
			DynamicRegistration bool `json:"dynamicRegistration,omitempty"`
		} `json:"didChangeWatchedFiles,omitempty"`
	} `json:"workspace,omitempty"`
	TextDocument *struct {
		Completion *struct {
			CompletionItem *struct {
				SnippetSupport bool `json:"snippetSupport,omitempty"`
			} `json:"completionItem,omitempty"`
		} `json:"completion,omitempty"`
		Hover *struct {
			ContentFormat []string `json:"contentFormat,omitempty"`
		} `json:"hover,omitempty"`
		PublishDiagnostics *struct {
			TagSupport *struct {
				ValueSet []int `json:"valueSet"`
			} `json:"tagSupport,omitempty"`
		} `json:"publishDiagnostics,omitempty"`
		InlineCompletion *struct {
			DynamicRegistration bool `json:"dynamicRegistration,omitempty"`
		} `json:"inlineCompletion,omitempty"`
	} `json:"textDocument,omitempty"`
}

type InitializeResult struct {
	Capabilities ServerCapabilities `json:"capabilities"`
	ServerInfo   *ServerInfo        `json:"serverInfo,omitempty"`
}

type ServerInfo struct {
	Name    string `json:"name"`
	Version string `json:"version,omitempty"`
}

// Text document sync kinds.
const (
	SyncNone        = 0
	SyncFull        = 1
	SyncIncremental = 2
)

type TextDocumentSyncOptions struct {
	OpenClose bool         `json:"openClose"`
	Change    int          `json:"change"`
	Save      *SaveOptions `json:"save,omitempty"`
}

type SaveOptions struct {
	IncludeText bool `json:"includeText"`
}

type ServerCapabilities struct {
	PositionEncoding        string                   `json:"positionEncoding,omitempty"`
	TextDocumentSync        *TextDocumentSyncOptions `json:"textDocumentSync,omitempty"`
	HoverProvider           bool                     `json:"hoverProvider,omitempty"`
	CompletionProvider      *CompletionOptions       `json:"completionProvider,omitempty"`
	DefinitionProvider      bool                     `json:"definitionProvider,omitempty"`
	ReferencesProvider      bool                     `json:"referencesProvider,omitempty"`
	RenameProvider          *RenameOptions           `json:"renameProvider,omitempty"`
	CodeActionProvider      *CodeActionOptions       `json:"codeActionProvider,omitempty"`
	ExecuteCommandProvider  *ExecuteCommandOptions   `json:"executeCommandProvider,omitempty"`
	DocumentSymbolProvider  bool                     `json:"documentSymbolProvider,omitempty"`
	WorkspaceSymbolProvider bool                     `json:"workspaceSymbolProvider,omitempty"`
	DocumentHighlight       bool                     `json:"documentHighlightProvider,omitempty"`
	FoldingRangeProvider    bool                     `json:"foldingRangeProvider,omitempty"`
	InlineCompletion        any                      `json:"inlineCompletionProvider,omitempty"`
	Workspace               *WorkspaceCapabilities   `json:"workspace,omitempty"`
}

type WorkspaceCapabilities struct {
	WorkspaceFolders *struct {
		Supported           bool `json:"supported"`
		ChangeNotifications bool `json:"changeNotifications"`
	} `json:"workspaceFolders,omitempty"`
}

type CompletionOptions struct {
	TriggerCharacters []string `json:"triggerCharacters,omitempty"`
	ResolveProvider   bool     `json:"resolveProvider,omitempty"`
}

type RenameOptions struct {
	PrepareProvider bool `json:"prepareProvider"`
}

type CodeActionOptions struct {
	CodeActionKinds []string `json:"codeActionKinds,omitempty"`
}

type ExecuteCommandOptions struct {
	Commands []string `json:"commands"`
}

// ---------------------------------------------------------------------------
// Documents

type TextDocumentIdentifier struct {
	URI DocumentURI `json:"uri"`
}

type VersionedTextDocumentIdentifier struct {
	URI     DocumentURI `json:"uri"`
	Version int         `json:"version"`
}

type OptionalVersionedTextDocumentIdentifier struct {
	URI     DocumentURI `json:"uri"`
	Version *int        `json:"version"`
}

type TextDocumentItem struct {
	URI        DocumentURI `json:"uri"`
	LanguageID string      `json:"languageId"`
	Version    int         `json:"version"`
	Text       string      `json:"text"`
}

type DidOpenTextDocumentParams struct {
	TextDocument TextDocumentItem `json:"textDocument"`
}

// TextDocumentContentChangeEvent is a full replacement when Range is nil.
type TextDocumentContentChangeEvent struct {
	Range *Range `json:"range,omitempty"`
	Text  string `json:"text"`
}

type DidChangeTextDocumentParams struct {
	TextDocument   VersionedTextDocumentIdentifier  `json:"textDocument"`
	ContentChanges []TextDocumentContentChangeEvent `json:"contentChanges"`
}

type DidCloseTextDocumentParams struct {
	TextDocument TextDocumentIdentifier `json:"textDocument"`
}

type DidSaveTextDocumentParams struct {
	TextDocument TextDocumentIdentifier `json:"textDocument"`
	Text         *string                `json:"text,omitempty"`
}

// File change types.
const (
	FileCreated = 1
	FileChanged = 2
	FileDeleted = 3
)

type FileEvent struct {
	URI  DocumentURI `json:"uri"`
	Type int         `json:"type"`
}

type DidChangeWatchedFilesParams struct {
	Changes []FileEvent `json:"changes"`
}

type DidChangeConfigurationParams struct {
	Settings json.RawMessage `json:"settings"`
}

type TextDocumentPositionParams struct {
	TextDocument TextDocumentIdentifier `json:"textDocument"`
	Position     Position               `json:"position"`
}

// ---------------------------------------------------------------------------
// Diagnostics

const (
	SeverityError       = 1
	SeverityWarning     = 2
	SeverityInformation = 3
	SeverityHint        = 4
)

const (
	TagUnnecessary = 1
	TagDeprecated  = 2
)

type Diagnostic struct {
	Range    Range           `json:"range"`
	Severity int             `json:"severity,omitempty"`
	Code     string          `json:"code,omitempty"`
	Source   string          `json:"source,omitempty"`
	Message  string          `json:"message"`
	Tags     []int           `json:"tags,omitempty"`
	Data     json.RawMessage `json:"data,omitempty"`
}

type PublishDiagnosticsParams struct {
	URI         DocumentURI  `json:"uri"`
	Version     *int         `json:"version,omitempty"`
	Diagnostics []Diagnostic `json:"diagnostics"`
}

// ---------------------------------------------------------------------------
// Language features

type MarkupContent struct {
	Kind  string `json:"kind"` // "markdown" or "plaintext"
	Value string `json:"value"`
}

type Hover struct {
	Contents MarkupContent `json:"contents"`
	Range    *Range        `json:"range,omitempty"`
}

type CompletionParams struct {
	TextDocumentPositionParams
	Context *struct {
		TriggerKind      int    `json:"triggerKind"`
		TriggerCharacter string `json:"triggerCharacter,omitempty"`
	} `json:"context,omitempty"`
}

// Completion item kinds used by the server.
const (
	CompletionKindText     = 1
	CompletionKindVariable = 6
	CompletionKindClass    = 7
	CompletionKindProperty = 10
	CompletionKindValue    = 12
	CompletionKindSnippet  = 15
	CompletionKindFile     = 17
	CompletionKindFunction = 3
)

const (
	InsertTextFormatPlainText = 1
	InsertTextFormatSnippet   = 2
)

type CompletionItem struct {
	Label            string         `json:"label"`
	Kind             int            `json:"kind,omitempty"`
	Detail           string         `json:"detail,omitempty"`
	Documentation    *MarkupContent `json:"documentation,omitempty"`
	SortText         string         `json:"sortText,omitempty"`
	FilterText       string         `json:"filterText,omitempty"`
	InsertTextFormat int            `json:"insertTextFormat,omitempty"`
	TextEdit         *TextEdit      `json:"textEdit,omitempty"`
	Command          *Command       `json:"command,omitempty"`
}

type CompletionList struct {
	IsIncomplete bool             `json:"isIncomplete"`
	Items        []CompletionItem `json:"items"`
}

type ReferenceParams struct {
	TextDocumentPositionParams
	Context struct {
		IncludeDeclaration bool `json:"includeDeclaration"`
	} `json:"context"`
}

type RenameParams struct {
	TextDocumentPositionParams
	NewName string `json:"newName"`
}

type PrepareRenameResult struct {
	Range       Range  `json:"range"`
	Placeholder string `json:"placeholder"`
}

type TextEdit struct {
	Range   Range  `json:"range"`
	NewText string `json:"newText"`
}

type TextDocumentEdit struct {
	TextDocument OptionalVersionedTextDocumentIdentifier `json:"textDocument"`
	Edits        []TextEdit                              `json:"edits"`
}

type CreateFile struct {
	Kind    string             `json:"kind"` // "create"
	URI     DocumentURI        `json:"uri"`
	Options *CreateFileOptions `json:"options,omitempty"`
}

type CreateFileOptions struct {
	Overwrite      bool `json:"overwrite,omitempty"`
	IgnoreIfExists bool `json:"ignoreIfExists,omitempty"`
}

// WorkspaceEdit uses Changes, or DocumentChanges whose elements are
// TextDocumentEdit or CreateFile values.
type WorkspaceEdit struct {
	Changes         map[DocumentURI][]TextEdit `json:"changes,omitempty"`
	DocumentChanges []any                      `json:"documentChanges,omitempty"`
}

type ApplyWorkspaceEditParams struct {
	Label string        `json:"label,omitempty"`
	Edit  WorkspaceEdit `json:"edit"`
}

type ApplyWorkspaceEditResult struct {
	Applied       bool   `json:"applied"`
	FailureReason string `json:"failureReason,omitempty"`
}

// Code action kinds.
const (
	CodeActionQuickFix = "quickfix"
	CodeActionSource   = "source"
	CodeActionRefactor = "refactor"
)

type CodeActionParams struct {
	TextDocument TextDocumentIdentifier `json:"textDocument"`
	Range        Range                  `json:"range"`
	Context      struct {
		Diagnostics []Diagnostic `json:"diagnostics"`
		Only        []string     `json:"only,omitempty"`
	} `json:"context"`
}

type CodeAction struct {
	Title       string         `json:"title"`
	Kind        string         `json:"kind,omitempty"`
	Diagnostics []Diagnostic   `json:"diagnostics,omitempty"`
	IsPreferred bool           `json:"isPreferred,omitempty"`
	Edit        *WorkspaceEdit `json:"edit,omitempty"`
	Command     *Command       `json:"command,omitempty"`
}

type Command struct {
	Title     string `json:"title"`
	Command   string `json:"command"`
	Arguments []any  `json:"arguments,omitempty"`
}

type ExecuteCommandParams struct {
	Command   string            `json:"command"`
	Arguments []json.RawMessage `json:"arguments,omitempty"`
}

// ---------------------------------------------------------------------------
// Inline completion (LSP 3.18)

const (
	InlineCompletionTriggerInvoked   = 1
	InlineCompletionTriggerAutomatic = 2
)

type InlineCompletionParams struct {
	TextDocumentPositionParams
	Context struct {
		TriggerKind int `json:"triggerKind"`
	} `json:"context"`
}

type InlineCompletionItem struct {
	InsertText string   `json:"insertText"`
	FilterText string   `json:"filterText,omitempty"`
	Range      *Range   `json:"range,omitempty"`
	Command    *Command `json:"command,omitempty"`
}

type InlineCompletionList struct {
	Items []InlineCompletionItem `json:"items"`
}

// ---------------------------------------------------------------------------
// Window

const (
	MessageError   = 1
	MessageWarning = 2
	MessageInfo    = 3
	MessageLog     = 4
)

type LogMessageParams struct {
	Type    int    `json:"type"`
	Message string `json:"message"`
}

type ShowMessageParams struct {
	Type    int    `json:"type"`
	Message string `json:"message"`
}

// ---------------------------------------------------------------------------
// Symbols, highlights, folding

// Symbol kinds used by the server.
const (
	SymbolKindModule    = 2
	SymbolKindNamespace = 3
	SymbolKindClass     = 5
	SymbolKindMethod    = 6
	SymbolKindProperty  = 7
	SymbolKindInterface = 11
	SymbolKindFunction  = 12
	SymbolKindVariable  = 13
	SymbolKindConstant  = 14
	SymbolKindEvent     = 24
)

type DocumentSymbolParams struct {
	TextDocument TextDocumentIdentifier `json:"textDocument"`
}

type DocumentSymbol struct {
	Name           string           `json:"name"`
	Detail         string           `json:"detail,omitempty"`
	Kind           int              `json:"kind"`
	Range          Range            `json:"range"`
	SelectionRange Range            `json:"selectionRange"`
	Children       []DocumentSymbol `json:"children,omitempty"`
}

type WorkspaceSymbolParams struct {
	Query string `json:"query"`
}

type SymbolInformation struct {
	Name          string   `json:"name"`
	Kind          int      `json:"kind"`
	Location      Location `json:"location"`
	ContainerName string   `json:"containerName,omitempty"`
}

// Document highlight kinds.
const (
	HighlightText  = 1
	HighlightRead  = 2
	HighlightWrite = 3
)

type DocumentHighlight struct {
	Range Range `json:"range"`
	Kind  int   `json:"kind,omitempty"`
}

type FoldingRangeParams struct {
	TextDocument TextDocumentIdentifier `json:"textDocument"`
}

type FoldingRange struct {
	StartLine uint32 `json:"startLine"`
	EndLine   uint32 `json:"endLine"`
	Kind      string `json:"kind,omitempty"`
}
