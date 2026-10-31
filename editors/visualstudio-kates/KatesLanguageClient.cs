using System;
using System.Collections.Generic;
using System.ComponentModel.Composition;
using System.Diagnostics;
using System.Threading;
using System.Threading.Tasks;
using Microsoft.VisualStudio.LanguageServer.Client;
using Microsoft.VisualStudio.Threading;
using Microsoft.VisualStudio.Utilities;

namespace Kates.VisualStudio
{
    [ContentType("yaml")]
    [Export(typeof(ILanguageClient))]
    public sealed class KatesLanguageClient : ILanguageClient
    {
        public string Name => "Kates YAML Language Server";

        public IEnumerable<string> ConfigurationSections => new[] { "kates" };

        public object InitializationOptions => null;

        public IEnumerable<string> FilesToWatch => new[] { "**/*.yaml", "**/*.yml", "**/*.json" };

        public bool ShowNotificationOnInitializeFailed => true;

        public event AsyncEventHandler<EventArgs> StartAsync;
        public event AsyncEventHandler<EventArgs> StopAsync;

        public async Task<Connection> ActivateAsync(CancellationToken token)
        {
            await Task.Yield();

            var executable = Environment.GetEnvironmentVariable("KATES_CLI_PATH") ?? "kates";
            var info = new ProcessStartInfo
            {
                FileName = executable,
                Arguments = "lsp --stdio",
                RedirectStandardInput = true,
                RedirectStandardOutput = true,
                RedirectStandardError = true,
                UseShellExecute = false,
                CreateNoWindow = true,
            };

            var process = new Process { StartInfo = info };
            if (!process.Start())
            {
                return null;
            }

            return new Connection(process.StandardOutput.BaseStream, process.StandardInput.BaseStream);
        }

        public async Task OnLoadedAsync()
        {
            if (StartAsync != null)
            {
                await StartAsync.InvokeAsync(this, EventArgs.Empty);
            }
        }

        public Task OnServerInitializedAsync() => Task.CompletedTask;

        public Task<InitializationFailureContext> OnServerInitializeFailedAsync(ILanguageClientInitializationInfo initializationState)
        {
            return Task.FromResult(new InitializationFailureContext
            {
                FailureMessage = $"Failed to start 'kates lsp --stdio'. Ensure 'kates' is on PATH or set KATES_CLI_PATH. ({initializationState.StatusMessage})"
            });
        }
    }
}
