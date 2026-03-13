---
name: system-info
description: Use when the user asks about system status, resource usage (disk, memory, CPU, network, battery), running processes, or service management. Covers diagnostics and process control.
tags: [macos, linux, system, diagnostics, process]
---
# System Info & Process Management

Query system information and manage processes. Cross-platform where noted.

## System Overview

macOS version and hardware:
```
exec: sw_vers && echo "---" && system_profiler SPHardwareDataType 2>/dev/null | grep -E "Model|Chip|Memory|Serial"
```

Uptime:
```
exec: uptime
```

## Disk Usage

Overview:
```
exec: df -h
```

Directory size breakdown:
```
exec: du -sh ~/* 2>/dev/null | sort -hr | head -20
```

Storage devices (macOS):
```
exec: diskutil list
```

## Memory Usage

macOS:
```
exec: vm_stat | perl -ne '/page size of (\d+)/ and $size=$1; /Pages\s+(\w+):\s+(\d+)/ and printf("%-16s %6.1f MB\n", "$1:", $2 * $size / 1048576)'
```

Total RAM:
```
exec: sysctl hw.memsize | awk '{printf "Total RAM: %.1f GB\n", $2/1073741824}'
```

Linux:
```
exec: free -h
```

Top memory consumers:
```
exec: ps aux --sort=-%mem | head -11
```

## CPU Info

CPU model and cores (macOS):
```
exec: sysctl -n machdep.cpu.brand_string && echo "Cores: $(sysctl -n hw.ncpu) ($(sysctl -n hw.physicalcpu) physical)"
```

Current load:
```
exec: top -l 1 -n 0 | head -10 2>/dev/null || top -bn1 | head -10
```

Top CPU consumers:
```
exec: ps aux --sort=-%cpu | head -11
```

## Network Info

Active interfaces:
```
exec: ifconfig | grep -E "^[a-z]|inet " | grep -B1 "inet "
```

Public IP:
```
exec: curl -s ifconfig.me
```

DNS servers:
```
exec: scutil --dns | grep nameserver | head -5
```

## Battery (macOS Laptops)

```
exec: pmset -g batt
```

## Process Management

List processes:
```
exec: ps aux | head -30
```

Find by name:
```
exec: pgrep -l "PROCESS_NAME"
```

Find by port:
```
exec: lsof -i :PORT_NUMBER
```

Kill by PID:
```
exec: kill PID
```

Kill by name:
```
exec: pkill "PROCESS_NAME"
```

Kill by port:
```
exec: lsof -ti :PORT_NUMBER | xargs kill -9 2>/dev/null && echo "Killed" || echo "No process on port PORT_NUMBER"
```

Open files by process:
```
exec: lsof -p PID | head -30
```

## Service Management (Linux systemd)

```
exec: systemctl status SERVICE_NAME
```

```
exec: systemctl list-units --type=service --state=running
```

## Notes

- `ps aux` works on both macOS and Linux.
- `top` flags differ: Linux uses `-b -n 1`, macOS uses `-l 1`.
- `free` is Linux-only; use `vm_stat` on macOS.
- `systemctl` is Linux-only; macOS uses `launchctl`.
- Some operations (kill, systemctl) may require `sudo`.
