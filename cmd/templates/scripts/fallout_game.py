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

from fallout import build_parser


def main():
    parser = build_parser()

    if len(sys.argv) < 2:
        parser.print_help()
        sys.exit(0)

    args = parser.parse_args()
    if not hasattr(args, "func"):
        print(json.dumps({"error": f"Unknown command: {sys.argv[1]}",
                          "hint": "Run without arguments to see available commands"}))
        sys.exit(1)

    args.func(args)


if __name__ == "__main__":
    main()
