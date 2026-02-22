"""World state management: init, status, set, turn."""

import json
import random
from .util import (
    error, ok, output, parse_int, load_state, save_state, require_state,
    get_effective_special, get_mode, enter_combat, exit_combat,
)


def cmd_init(args):
    """Initialize a new game state."""
    state = {
        "chapter": 1,
        "chapter_title": "Leaving the Vault",
        "chapter_start_turn": 0,
        "location": "Vault 111",
        "turn": 0,
        "time_of_day": "Early Morning",
        "weather": "Clear",
        "quest": "Escape the vault",
        "players": {},
        "enemies": {},
        "mode": "exploration",
        "combat_round": 0,
        "turn_actions": {},
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
            effective, modifiers = get_effective_special(player)
            result = {"ok": True, "player": name, **player}
            if effective != player.get("special", {}):
                result["effective_special"] = effective
                result["special_modifiers"] = {
                    attr: [{"source": src, "mod": mod} for src, mod in mods]
                    for attr, mods in modifiers.items()
                }
            output(result, indent=True)
        else:
            available = list(state.get("players", {}).keys())
            error(f"Player not found: {name}", available_players=available)
    else:
        # Full game state: add effective SPECIAL for each player
        result = {"ok": True, **state}
        for pname, player in state.get("players", {}).items():
            effective, modifiers = get_effective_special(player)
            if effective != player.get("special", {}):
                result["players"][pname]["effective_special"] = effective

        # Action tracking summary in combat mode
        if get_mode(state) == "combat":
            actions = state.get("turn_actions", {})
            expected = set()
            for pname, player in state.get("players", {}).items():
                if player.get("hp", 0) > 0:
                    expected.add(pname)
            for ename, enemy in state.get("enemies", {}).items():
                if enemy.get("status") == "alive":
                    expected.add(ename)
            pending = sorted(expected - set(actions.keys()))
            result["action_summary"] = {
                "acted": sorted(actions.keys()),
                "pending": pending,
                "all_acted": len(pending) == 0,
            }

        output(result, indent=True)


def cmd_set(args):
    """Set a game state field.
    Usage: set <field> <value>
    Fields: chapter, location, quest, time_of_day, weather, chapter_title, mode
    """
    allowed = ["chapter", "location", "quest", "time_of_day", "weather", "chapter_title", "mode"]

    if len(args) < 2:
        return error("Usage: set <field> <value>", valid_fields=allowed,
                      hint="Example: set location 'Diamond City' | set chapter 2 | set weather Rainstorm")

    state = require_state()
    if not state:
        return

    field = args[0]
    value = " ".join(args[1:])

    if field not in allowed:
        return error(f"Invalid field: {field}", valid_fields=allowed,
                      hint="Choose from the valid fields list above.")

    if field == "chapter":
        value = parse_int(value, "chapter")
        if value is None:
            return

    if field == "mode":
        if value not in ("exploration", "combat"):
            return error(f"Mode must be 'exploration' or 'combat', got: {value}",
                          hint="exploration: free roam, time advances with 'turn'. combat: round-based, auto-enters when enemies added.")
        state["turn_actions"] = {}

    old = state.get(field)
    state[field] = value

    # Track when chapter changes for encounter safe_turns
    if field == "chapter":
        state["chapter_start_turn"] = state.get("turn", 0)

    save_state(state)
    ok(f"Set {field}", field=field, old_value=old, new_value=value)


# ---------------------------------------------------------------------------
# Turn (mode-aware)
# ---------------------------------------------------------------------------

def cmd_turn(args):
    """Advance turn (mode-aware).
    Exploration: full turn (time, weather, effects, random events).
    Combat: combat round only (tick effects, clear dead, check combat end).
    """
    state = require_state()
    if not state:
        return

    if get_mode(state) == "combat":
        _combat_turn(state)
    else:
        _exploration_turn(state)


def _tick_effects(state):
    """Tick status effects for all players. Returns (expired, active, deaths)."""
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

    # Check for death: Incapacitated expired while HP still 0
    deaths = []
    for exp in expired_effects:
        if exp["effect"] == "Incapacitated":
            p = state["players"].get(exp["player"])
            if p and p["hp"] <= 0:
                deaths.append(exp["player"])

    return expired_effects, active_effects, deaths


def _exploration_turn(state):
    """Full exploration turn: increment turn, cycle time, weather, tick effects, random events."""
    state["turn"] = state.get("turn", 0) + 1

    # Cycle time of day
    times = ["Early Morning", "Morning", "Noon", "Afternoon", "Evening", "Night", "Late Night", "Pre-Dawn"]
    current = state.get("time_of_day", "Early Morning")
    current_idx = times.index(current) if current in times else 0
    new_time = times[(current_idx + 1) % len(times)]
    state["time_of_day"] = new_time

    # Auto-generate weather on new day (Early Morning)
    weather_changed = None
    if new_time == "Early Morning":
        from .data import WEATHER_TABLE
        total = sum(w["weight"] for w in WEATHER_TABLE)
        roll = random.randint(1, total)
        cumulative = 0
        chosen = WEATHER_TABLE[0]
        for w in WEATHER_TABLE:
            cumulative += w["weight"]
            if roll <= cumulative:
                chosen = w
                break
        state["weather"] = chosen["weather"]
        weather_changed = {"weather": chosen["weather"], "description": chosen["desc"], "effect": chosen["effect"]}

    # Tick effects
    expired_effects, active_effects, deaths = _tick_effects(state)

    result = {
        "ok": True,
        "mode": "exploration",
        "turn": state["turn"],
        "time_of_day": state["time_of_day"],
        "chapter": state["chapter"],
    }
    if weather_changed:
        result["weather_changed"] = weather_changed
    if deaths:
        result["deaths"] = deaths
        result["death_warning"] = "Players have died! They were not stabilized in time."
    if expired_effects:
        result["expired_effects"] = expired_effects
    if active_effects:
        result["active_effects"] = active_effects

    # Auto-clear dead enemies
    enemies = state.get("enemies", {})
    dead = [n for n, e in enemies.items() if e["status"] == "dead"]
    for n in dead:
        del enemies[n]
    if dead:
        result["enemies_cleared"] = dead

    # Report alive enemies
    alive = [{"name": n, "hp": f"{e['hp']}/{e['max_hp']}"} for n, e in enemies.items() if e["status"] == "alive"]
    if alive:
        result["enemies_alive"] = alive

    # Random event (10% chance, skip if enemies alive)
    if not alive and random.randint(1, 100) <= 10:
        from .data import ENCOUNTERS, ATMOSPHERIC, QUEST_HOOKS
        roll = random.randint(1, 100)
        if roll <= 70:
            pool = []
            for v in ENCOUNTERS.values():
                pool.extend(v)
            event = random.choice(pool)
            result["random_event"] = {"type": "encounter", "event": event}
        elif roll <= 85:
            result["random_event"] = {"type": "atmospheric", "event": random.choice(ATMOSPHERIC)}
        else:
            result["random_event"] = {"type": "quest_hook", "event": random.choice(QUEST_HOOKS)}
        result["random_event"]["note"] = "GM: check if this fits the current narrative. Ignore if it doesn't."

    # Reset action tracking
    state["turn_actions"] = {}

    save_state(state)
    output(result, indent=True)


def _combat_turn(state):
    """Combat round: tick effects, clear dead, check combat end. No time advancement."""
    state["combat_round"] = state.get("combat_round", 0) + 1

    # Tick effects
    expired_effects, active_effects, deaths = _tick_effects(state)

    result = {
        "ok": True,
        "mode": "combat",
        "combat_round": state["combat_round"],
        "turn": state.get("turn", 0),
    }
    if deaths:
        result["deaths"] = deaths
        result["death_warning"] = "Players have died! They were not stabilized in time."
    if expired_effects:
        result["expired_effects"] = expired_effects
    if active_effects:
        result["active_effects"] = active_effects

    # Auto-clear dead enemies
    enemies = state.get("enemies", {})
    dead = [n for n, e in enemies.items() if e["status"] == "dead"]
    for n in dead:
        del enemies[n]
    if dead:
        result["enemies_cleared"] = dead

    # Report alive enemies
    alive = [{"name": n, "hp": f"{e['hp']}/{e['max_hp']}"} for n, e in enemies.items() if e["status"] == "alive"]
    if alive:
        result["enemies_alive"] = alive

    # Auto exit combat if no enemies remain
    if not alive:
        transition = exit_combat(state)
        if transition:
            result.update(transition)

    # Reset action tracking
    state["turn_actions"] = {}

    save_state(state)
    output(result, indent=True)
