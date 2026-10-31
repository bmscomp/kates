using System;
using System.Runtime.InteropServices;
using System.Threading;
using Kates.VisualStudio.Commands;
using Microsoft.VisualStudio.Shell;
using Task = System.Threading.Tasks.Task;

namespace Kates.VisualStudio
{
    [PackageRegistration(UseManagedResourcesOnly = true, AllowsBackgroundLoading = true)]
    [ProvideMenuResource("Menus.ctmenu", 1)]
    [Guid(PackageGuidString)]
    public sealed class KatesPackage : AsyncPackage
    {
        public const string PackageGuidString = "2d6e52a1-92f1-4926-9951-5d912f4488a0";

        protected override async Task InitializeAsync(CancellationToken cancellationToken, IProgress<ServiceProgressData> progress)
        {
            await base.InitializeAsync(cancellationToken, progress);
            await RunKatesCommand.InitializeAsync(this);
        }
    }
}
