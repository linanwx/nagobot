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

# Radiation penalty tiers (first match wins, not cumulative)
RAD_PENALTIES = [
    (1000, {"STR": -4, "PER": -3, "END": -4, "AGI": -3, "LCK": -3}),
    ( 800, {"STR": -3, "PER": -2, "END": -3, "AGI": -2}),
    ( 600, {"STR": -2, "PER": -1, "END": -2, "AGI": -1}),
    ( 400, {"STR": -1, "END": -1}),
    ( 200, {"END": -1}),
]

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

def require_enemy(state, name):
    """Get enemy from state or print error. Returns enemy or None."""
    enemy = state.get("enemies", {}).get(name)
    if not enemy:
        available = list(state.get("enemies", {}).keys())
        error(f"Enemy not found: {name}", available_enemies=available)
    return enemy


def get_effective_special(player):
    """Compute effective SPECIAL values after radiation and drug modifiers.
    Never mutates stored player["special"].
    Returns (effective_dict, modifiers_dict).
    modifiers_dict maps attr -> list of (source, amount) tuples.
    """
    base = dict(player.get("special", {}))
    effective = dict(base)
    modifiers = {}

    # Radiation penalties (first matching tier)
    rads = player.get("rads", 0)
    for threshold, penalties in RAD_PENALTIES:
        if rads >= threshold:
            for attr, mod in penalties.items():
                modifiers.setdefault(attr, []).append((f"Radiation ({rads} rads)", mod))
                effective[attr] = effective.get(attr, 0) + mod
            break

    # Drug/status effect bonuses
    for effect in player.get("status_effects", []):
        stat_mods = effect.get("stat_mods", {})
        source = effect.get("name", "Unknown")
        for attr, mod in stat_mods.items():
            modifiers.setdefault(attr, []).append((source, mod))
            effective[attr] = effective.get(attr, 0) + mod

    # Clamp 1-10
    for attr in effective:
        effective[attr] = max(1, min(10, effective[attr]))

    return effective, modifiers


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


# ---------------------------------------------------------------------------
# Mode & action tracking
# ---------------------------------------------------------------------------

def get_mode(state):
    """Return current game mode. Defaults to 'exploration' for old states."""
    return state.get("mode", "exploration")


def enter_combat(state):
    """Transition to combat mode. Returns transition info or None if already in combat."""
    if get_mode(state) == "combat":
        return None
    state["mode"] = "combat"
    state["combat_round"] = 0
    state["turn_actions"] = {}
    return {"mode_changed": {"from": "exploration", "to": "combat"},
            "hint": "Combat initiated! Call 'initiative' for turn order."}


def exit_combat(state):
    """Transition to exploration mode. Returns transition info or None if already exploring."""
    if get_mode(state) == "exploration":
        return None
    state["mode"] = "exploration"
    state["combat_round"] = 0
    state["turn_actions"] = {}
    return {"mode_changed": {"from": "combat", "to": "exploration"},
            "hint": "Combat ended. Returning to exploration."}


def register_action(state, actor_name):
    """Register that an actor has acted this round.
    Returns action_status dict with mode, pending list, and all_acted flag.
    """
    actions = state.setdefault("turn_actions", {})
    actions[actor_name] = True

    # Build expected actors: living players + alive enemies
    expected = set()
    for pname, player in state.get("players", {}).items():
        if player.get("hp", 0) > 0:
            expected.add(pname)
    for ename, enemy in state.get("enemies", {}).items():
        if enemy.get("status") == "alive":
            expected.add(ename)

    pending = sorted(expected - set(actions.keys()))
    all_acted = len(pending) == 0

    result = {"mode": get_mode(state), "pending": pending, "all_acted": all_acted}
    if all_acted:
        result["hint"] = "All units have acted. Call 'turn' to advance."
    return result
