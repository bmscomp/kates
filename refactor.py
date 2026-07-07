import sys
import re

def process_file(filename):
    with open(filename, 'r') as f:
        content = f.read()

    lines = content.split('\n')
    new_lines = []
    i = 0
    while i < len(lines):
        line = lines[i]
        if 'if outputMode == "json" {' in line:
            # Extract the variable from the next line
            next_line = lines[i+1]
            m = re.search(r'output\.JSON\(([^)]+)\)', next_line)
            if not m:
                new_lines.append(line)
                i += 1
                continue
            var_name = m.group(1)
            
            # Skip the 'return nil' and '}'
            i += 4
            
            indent = line.split('if')[0]
            new_lines.append(f'{indent}output.Render(outputMode == "json", {var_name}, func() {{')
            
            body = []
            while i < len(lines):
                if lines[i] == f'{indent}return nil':
                    # Add extra tab to body
                    for b in body:
                        if b == '':
                            new_lines.append(b)
                        else:
                            new_lines.append('\t' + b)
                    new_lines.append(f'{indent}}})')
                    new_lines.append(f'{indent}return nil')
                    break
                else:
                    body.append(lines[i])
                i += 1
        else:
            new_lines.append(line)
        i += 1

    with open(filename, 'w') as f:
        f.write('\n'.join(new_lines))

for arg in sys.argv[1:]:
    process_file(arg)
