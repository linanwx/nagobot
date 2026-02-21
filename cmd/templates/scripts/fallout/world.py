"""World state management: init, status, set, flag, turn, log."""

import json
from .util import error, ok, output, parse_int, load_state, save_state, require_state


def cmd_init(args):
    """Initialize a new game state."""
    state = {
        "chapter": 1,
        "chapter_title": "Leaving the Vault",
        "location": "Vault 111",
        "turn": 0,
        "time_of_day": "Early Morning",
        "weather": "Clear",
        "quest": "Escape the vault",
        "flags": [],
        "event_log": [],
        "players": {},
    }
    save_state(state)
    ok("New game initialized", state=state)


def cmd_status(args):
    """View game or player status.
    Usage: status [player_name]
    """
    state = require_state()
    if not state:
        return

    if args:
        name = " ".join(args)
        player = state.get("players", {}).get(name)
        if player:
            output({"ok": True, "player": name, **player}, indent=True)
        else:
            available = list(state.get("players", {}).keys())
            error(f"Player not found: {name}", available_players=available)
    else:
        output({"ok": True, **state}, indent=True)


def cmd_set(args):
    """Set a game state field.
    Usage: set <field> <value>
    Fields: chapter, location, quest, time_of_day, weather, chapter_title
    """
    allowed = ["chapter", "location", "quest", "time_of_day", "weather", "chapter_title"]

    if len(args) < 2:
        return error("Usage: set <field> <value>", valid_fields=allowed)

    state = require_state()
    if not state:
        return

    field = args[0]
    value = " ".join(args[1:])

    if field not in allowed:
        return error(f"Invalid field: {field}", valid_fields=allowed)

    if field == "chapter":
        value = parse_int(value, "chapter")
        if value is None:
            return

    old = state.get(field)
    state[field] = value
    save_state(state)
    ok(f"Set {field}", field=field, old_value=old, new_value=value)


def cmd_flag(args):
    """Manage story flags.
    Usage: flag add <flag_name>  |  flag remove <flag_name>  |  flag list
    """
    if not args:
        return error("Usage: flag add/remove/list [flag_name]")

    state = require_state()
    if not state:
        return

    action = args[0].lower()
    if action == "list":
        output({"ok": True, "flags": state.get("flags", [])})
    elif action == "add" and len(args) > 1:
        flag = " ".join(args[1:])
        state.setdefault("flags", [])
        if flag not in state["flags"]:
            state["flags"].append(flag)
            save_state(state)
        ok(f"Flag added: {flag}", flags=state["flags"])
    elif action == "remove" and len(args) > 1:
        flag = " ".join(args[1:])
        flags = state.get("flags", [])
        if flag in flags:
            flags.remove(flag)
            save_state(state)
        ok(f"Flag removed: {flag}", flags=flags)
    else:
        error("Usage: flag add/remove/list [flag_name]")


def cmd_turn(args):
    """Advance turn counter, cycle time of day, tick status effects."""
    state = require_state()
    if not state:
        return

    state["turn"] = state.get("turn", 0) + 1

    # Cycle time of day every 3 turns
    times = ["Early Morning", "Morning", "Noon", "Afternoon", "Evening", "Night", "Late Night", "Pre-Dawn"]
    current = state.get("time_of_day", "Early Morning")
    current_idx = times.index(current) if current in times else 0
    if state["turn"] % 3 == 0:
        state["time_of_day"] = times[(current_idx + 1) % len(times)]

    # Tick status effects — reduce durations, remove expired
    expired_effects = []
    active_effects = []
    for pname, player in state.get("players", {}).items():
        effects = player.get("status_effects", [])
        remaining = []
        for e in effects:
            if e.get("remaining", 0) == -1:  # permanent
                remaining.append(e)
                active_effects.append({"player": pname, "effect": e["name"], "remaining": "permanent"})
            elif e.get("remaining", 0) > 1:
                e["remaining"] -= 1
                remaining.append(e)
                active_effects.append({"player": pname, "effect": e["name"], "remaining": e["remaining"]})
            else:
                expired_effects.append({"player": pname, "effect": e["name"]})
        player["status_effects"] = remaining

    save_state(state)
    result = {
        "ok": True,
        "turn": state["turn"],
        "time_of_day": state["time_of_day"],
        "chapter": state["chapter"],
    }
    if expired_effects:
        result["expired_effects"] = expired_effects
    if active_effects:
        result["active_effects"] = active_effects
    output(result, indent=True)


def cmd_log(args):
    """Add an event to the log. Usage: log <event description>"""
    if not args:
        return error("Usage: log <event description>")

    state = require_state()
    if not state:
        return

    event = " ".join(args)
    state.setdefault("event_log", []).append({
        "turn": state.get("turn", 0),
        "event": event,
    })
    # Keep only last 50 events
    state["event_log"] = state["event_log"][-50:]
    save_state(state)
    ok(f"Logged: {event}")
