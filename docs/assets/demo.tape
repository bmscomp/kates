# VHS tape for the README demo recording (https://github.com/charmbracelet/vhs)
#
# Generate the GIF with a provisioned local stack running (kates deploy -i):
#   vhs docs/assets/demo.tape
# Then reference docs/assets/demo.gif from the README hero section.

Output docs/assets/demo.gif

Set FontSize 16
Set Width 1200
Set Height 640
Set Theme "Catppuccin Mocha"
Set TypingSpeed 40ms

Type "kates health"
Enter
Sleep 3s

Type "kates test create --type LOAD --records 100000"
Enter
Sleep 4s

Type "kates test list"
Enter
Sleep 3s

Type "kates resilience --experiment pod-kill --duration 60s"
Enter
Sleep 5s

Type "kates dashboard"
Enter
Sleep 6s
