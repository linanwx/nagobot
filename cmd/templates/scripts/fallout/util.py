"""Shared utilities: state I/O, output helpers, validation, dice."""

import json
import os
import random
from pathlib import Path

# ---------------------------------------------------------------------------
# Configuration
# ---------------------------------------------------------------------------

STATE_FILE = os.environ.get("FALLOUT_STATE", "fallout_game.json")

if not os.path.isabs(STATE_FILE):
    workspace = Path(__file__).resolve().parent.parent.parent
    STATE_FILE = str(workspace / STATE_FILE)

# ---------------------------------------------------------------------------
# Constants
# ---------------------------------------------------------------------------

ALL_SKILLS = [
    "Barter", "Lockpick", "Medicine", "Melee", "Repair",
    "Science", "Small Guns", "Sneak", "Speech", "Survival",
]

SPECIAL_ATTRS = ["STR", "PER", "END", "CHA", "INT", "AGI", "LCK"]

# ---------------------------------------------------------------------------
# Output helpers
# ---------------------------------------------------------------------------

def output(data, indent=False):
    """Print JSON output. Single output point for all commands."""
    print(json.dumps(data, ensure_ascii=False, indent=2 if indent else None))


def error(msg, **extra):
    """Print error JSON and return None (for early-return chaining)."""
    data = {"error": msg}
    data.update(extra)
    output(data)
    return None


def ok(msg=None, **extra):
    """Print success JSON."""
    data = {"ok": True}
    if msg:
        data["message"] = msg
    data.update(extra)
    output(data, indent=True)

# ---------------------------------------------------------------------------
# State I/O
# ---------------------------------------------------------------------------

def load_state():
    """Load game state from JSON file."""
    if not os.path.exists(STATE_FILE):
        return None
    with open(STATE_FILE, "r", encoding="utf-8") as f:
        return json.load(f)


def save_state(state):
    """Save game state to JSON file with backup."""
    os.makedirs(os.path.dirname(STATE_FILE) or ".", exist_ok=True)
    if os.path.exists(STATE_FILE):
        backup = STATE_FILE + ".bak"
        try:
            import shutil
            shutil.copy2(STATE_FILE, backup)
        except Exception:
            pass
    with open(STATE_FILE, "w", encoding="utf-8") as f:
        json.dump(state, f, ensure_ascii=False, indent=2)


def require_state():
    """Load state or print error. Returns state or None."""
    state = load_state()
    if state is None:
        error("Game not initialized. Run 'init' first.")
    return state


def require_player(state, name):
    """Get player from state or print error. Returns player or None."""
    player = state.get("players", {}).get(name)
    if not player:
        available = list(state.get("players", {}).keys())
        error(f"Player not found: {name}", available_players=available)
    return player

# ---------------------------------------------------------------------------
# Validation helpers
# ---------------------------------------------------------------------------

def validate_attr(name):
    """Validate SPECIAL attribute name (case-insensitive).
    Returns canonical name or None (prints error).
    """
    upper = name.upper()
    if upper in SPECIAL_ATTRS:
        return upper
    error(f"Invalid attribute: {name}", valid_attributes=SPECIAL_ATTRS)
    return None


def validate_skill(name):
    """Validate skill name (case-insensitive fuzzy match).
    Returns canonical name or None (prints error).
    """
    # Exact match first
    for s in ALL_SKILLS:
        if s.lower() == name.lower():
            return s
    # Partial match
    matches = [s for s in ALL_SKILLS if name.lower() in s.lower()]
    if len(matches) == 1:
        return matches[0]
    error(f"Invalid skill: {name}", valid_skills=ALL_SKILLS)
    return None


def parse_int(value, field_name="value"):
    """Parse integer or print error. Returns int or None."""
    try:
        return int(value)
    except (ValueError, TypeError):
        error(f"{field_name} must be a number, got: {value}")
        return None

# ---------------------------------------------------------------------------
# Dice
# ---------------------------------------------------------------------------

def roll_dice(count, sides):
    """Roll dice and return individual results."""
    return [random.randint(1, sides) for _ in range(count)]
