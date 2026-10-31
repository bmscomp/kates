using System;
using System.ComponentModel.Design;
using System.Diagnostics;
using System.IO;
using System.Threading.Tasks;
using EnvDTE;
using Microsoft.VisualStudio.Shell;
using Microsoft.VisualStudio.Shell.Interop;
using Process = System.Diagnostics.Process;
using Task = System.Threading.Tasks.Task;

namespace Kates.VisualStudio.Commands
{
    public sealed class RunKatesCommand
    {
        public static readonly Guid CommandSet = new Guid("8b4f7c10-5c11-4f88-9024-8e2e6f03d120");
        public const int LintCommandId = 0x0100;
        public const int RunCommandId = 0x0101;
        public const int DryRunCommandId = 0x0102;

        private readonly AsyncPackage _package;
        private static readonly Guid OutputPaneGuid = new Guid("9e6b21a4-33a9-4e11-8d2f-4c9a12b71099");

        private RunKatesCommand(AsyncPackage package, OleMenuCommandService commandService)
        {
            _package = package ?? throw new ArgumentNullException(nameof(package));
            commandService = commandService ?? throw new ArgumentNullException(nameof(commandService));

            Register(commandService, LintCommandId, OnLintFile);
            Register(commandService, RunCommandId, OnRunFile);
            Register(commandService, DryRunCommandId, OnDryRunFile);
        }

        public static async Task InitializeAsync(AsyncPackage package)
        {
            await ThreadHelper.JoinableTaskFactory.SwitchToMainThreadAsync(package.DisposalToken);
            var commandService = await package.GetServiceAsync(typeof(IMenuCommandService)) as OleMenuCommandService;
            if (commandService != null)
            {
                _ = new RunKatesCommand(package, commandService);
            }
        }

        private static void Register(OleMenuCommandService service, int id, EventHandler handler)
        {
            var menuCommandID = new CommandID(CommandSet, id);
            var menuItem = new MenuCommand(handler, menuCommandID);
            service.AddCommand(menuItem);
        }

        private void OnLintFile(object sender, EventArgs e)
        {
            ThreadHelper.ThrowIfNotOnUIThread();
            var path = GetActiveDocumentPath();
            if (!string.IsNullOrEmpty(path))
            {
                RunKatesCli(ResolveLintArgs(path));
            }
        }

        private void OnRunFile(object sender, EventArgs e)
        {
            ThreadHelper.ThrowIfNotOnUIThread();
            var path = GetActiveDocumentPath();
            if (!string.IsNullOrEmpty(path))
            {
                RunKatesCli(ResolveRunArgs(path, dryRun: false));
            }
        }

        private void OnDryRunFile(object sender, EventArgs e)
        {
            ThreadHelper.ThrowIfNotOnUIThread();
            var path = GetActiveDocumentPath();
            if (!string.IsNullOrEmpty(path))
            {
                RunKatesCli(ResolveRunArgs(path, dryRun: true));
            }
        }

        private string GetActiveDocumentPath()
        {
            ThreadHelper.ThrowIfNotOnUIThread();
            var dte = Package.GetGlobalService(typeof(DTE)) as DTE;
            return dte?.ActiveDocument?.FullName;
        }

        private static string ResolveLintArgs(string filePath)
        {
            var lower = filePath.ToLowerInvariant();
            if (lower.Contains("conformity") || lower.Contains("compliance") || lower.EndsWith(".kafkareference.yaml"))
                return $"compliance lint \"{filePath}\"";
            if (lower.Contains("flow"))
                return $"flow lint \"{filePath}\"";
            if (lower.Contains("resilience"))
                return $"resilience lint -f \"{filePath}\"";
            if (lower.Contains("disruption") || lower.Contains("playbook"))
                return $"disruption lint -f \"{filePath}\"";
            return $"test lint \"{filePath}\"";
        }

        private static string ResolveRunArgs(string filePath, bool dryRun)
        {
            var lower = filePath.ToLowerInvariant();
            if (lower.Contains("conformity") || lower.Contains("compliance") || lower.EndsWith(".kafkareference.yaml"))
                return $"compliance check -f \"{filePath}\"";
            if (lower.Contains("flow"))
                return $"flow run -f \"{filePath}\" --wait";
            if (lower.Contains("resilience"))
                return dryRun ? $"resilience run -f \"{filePath}\" --dry-run" : $"resilience run -f \"{filePath}\"";
            if (lower.Contains("disruption") || lower.Contains("playbook"))
                return dryRun ? $"disruption run --config \"{filePath}\" --dry-run" : $"disruption run --config \"{filePath}\"";
            return $"test apply -f \"{filePath}\" --wait";
        }

        private void RunKatesCli(string arguments)
        {
            ThreadHelper.ThrowIfNotOnUIThread();
            var outWindow = Package.GetGlobalService(typeof(SVsOutputWindow)) as IVsOutputWindow;
            if (outWindow == null) return;

            var paneGuid = OutputPaneGuid;
            outWindow.CreatePane(ref paneGuid, "Kates YAML Studio", 1, 1);
            outWindow.GetPane(ref paneGuid, out IVsOutputWindowPane pane);
            pane?.Activate();
            pane?.OutputStringThreadSafe($"> kates {arguments}{Environment.NewLine}");

            var executable = Environment.GetEnvironmentVariable("KATES_CLI_PATH") ?? "kates";
            _ = Task.Run(() =>
            {
                try
                {
                    var psi = new ProcessStartInfo
                    {
                        FileName = executable,
                        Arguments = arguments,
                        WorkingDirectory = Path.GetDirectoryName(GetActiveDocumentPath()) ?? Environment.CurrentDirectory,
                        RedirectStandardOutput = true,
                        RedirectStandardError = true,
                        UseShellExecute = false,
                        CreateNoWindow = true,
                    };
                    using (var proc = Process.Start(psi))
                    {
                        if (proc == null) return;
                        var stdout = proc.StandardOutput.ReadToEnd();
                        var stderr = proc.StandardError.ReadToEnd();
                        proc.WaitForExit();
                        if (!string.IsNullOrEmpty(stdout)) pane?.OutputStringThreadSafe(stdout);
                        if (!string.IsNullOrEmpty(stderr)) pane?.OutputStringThreadSafe(stderr);
                        pane?.OutputStringThreadSafe($"[kates exited with code {proc.ExitCode}]{Environment.NewLine}{Environment.NewLine}");
                    }
                }
                catch (Exception ex)
                {
                    pane?.OutputStringThreadSafe($"Error running kates: {ex.Message}{Environment.NewLine}");
                }
            });
        }
    }
}
