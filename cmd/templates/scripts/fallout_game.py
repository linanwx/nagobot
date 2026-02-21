#!/usr/bin/env python3
"""
Fallout Wasteland RPG — Game Engine
====================================
A comprehensive game utility for the Fallout text adventure agent.
Handles dice, skill checks, state management, random events, loot, and combat.

Usage:
    python3 scripts/fallout_game.py <command> [args...]

State file: fallout_game.json (in workspace root by default)
"""

import json
import sys

from fallout import COMMANDS


def main():
    if len(sys.argv) < 2:
        COMMANDS["help"]([])
        sys.exit(0)

    command = sys.argv[1].lower()
    args = sys.argv[2:]

    if command in COMMANDS:
        COMMANDS[command](args)
    else:
        print(json.dumps({
            "error": f"Unknown command: {command}",
            "hint": "Run 'help' to see available commands",
        }))
        sys.exit(1)


if __name__ == "__main__":
    main()
