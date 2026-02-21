"""Consumables, status effects, rest, and recovery."""

import os
import random
from .util import error, ok, output, parse_int, require_state, require_player, save_state, STATE_FILE
from .data import CHEM_EFFECTS


def cmd_use_item(args):
    """Use a consumable item.
    Usage: use-item <player> <item_name>
    """
    if len(args) < 2:
        return error("Usage: use-item <player> <item_name>",
                      hint=f"Known consumables: {', '.join(CHEM_EFFECTS.keys())}")

    state = require_state()
    if not state:
        return

    name = args[0]
    item = " ".join(args[1:])

    player = require_player(state, name)
    if not player:
        return

    if item not in player.get("inventory", []):
        return error(f"Player {name} does not have item: {item}",
                      inventory=player.get("inventory", []))

    chem = CHEM_EFFECTS.get(item)
    if not chem:
        return error(f"Unknown consumable: {item}. This item cannot be used — it may be equipment or a crafting material.",
                      known_consumables=list(CHEM_EFFECTS.keys()))

    # Remove from inventory
    player["inventory"].remove(item)

    results = {"ok": True, "player": name, "item": item, "effects": []}

    if "heal" in chem:
        old_hp = player["hp"]
        player["hp"] = min(player["max_hp"], player["hp"] + chem["heal"])
        results["effects"].append(f"HP: {old_hp} -> {player['hp']}")

    if "rads" in chem:
        old_rads = player.get("rads", 0)
        player["rads"] = max(0, old_rads + chem["rads"])
        results["effects"].append(f"Rads: {old_rads} -> {player['rads']}")

    if "ap" in chem:
        old_ap = player.get("ap", 0)
        player["ap"] = old_ap + chem["ap"]
        results["effects"].append(f"AP: {old_ap} -> {player['ap']}")

    if "effect" in chem:
        effect_entry = {
            "name": chem["effect"],
            "remaining": chem.get("duration", 1),
            "source": item,
        }
        player.setdefault("status_effects", []).append(effect_entry)
        results["effects"].append(f"Status gained: {chem['effect']} ({chem.get('duration', 1)} rounds)")

        # Addiction check for chems
        if "Addiction risk" in chem.get("desc", ""):
            addiction_roll = random.randint(1, 20)
            if addiction_roll <= 3:
                player.setdefault("status_effects", []).append({
                    "name": f"{item} Addiction",
                    "remaining": -1,
                    "source": "addiction",
                })
                results["effects"].append(f"WARNING: Addicted! (Roll: {addiction_roll}, needed >3)")
                results["addicted"] = True
            else:
                results["effects"].append(f"Not addicted (Roll: {addiction_roll}, needed <=3)")

    results["description"] = chem["desc"]
    save_state(state)
    output(results, indent=True)


def cmd_effect(args):
    """Manage status effects.
    Usage: effect <player> add <name> <duration>
           effect <player> remove <name>
           effect <player> list
    Note: effects are automatically ticked down by the 'turn' command.
    """
    if not args:
        return error("Usage: effect <player> add/remove/list <name> [duration]")

    state = require_state()
    if not state:
        return

    # Legacy support: 'effect tick' redirects to a note
    if args[0] == "tick":
        return error("'effect tick' is deprecated. Use 'turn' command which automatically ticks effects.",
                      hint="Run: python3 scripts/fallout_game.py turn")

    if len(args) < 2:
        return error("Usage: effect <player> add/remove/list <name> [duration]")

    name = args[0]
    action = args[1].lower()

    player = require_player(state, name)
    if not player:
        return

    if action == "list":
        effects = player.get("status_effects", [])
        output({"ok": True, "player": name, "effects": effects}, indent=True)
    elif action == "add" and len(args) >= 4:
        effect_name = args[2]
        duration = parse_int(args[3], "duration")
        if duration is None:
            return
        player.setdefault("status_effects", []).append({
            "name": effect_name,
            "remaining": duration,
            "source": "gm",
        })
        save_state(state)
        ok(f"Added effect: {effect_name} ({duration} turns)", player=name)
    elif action == "remove" and len(args) >= 3:
        effect_name = args[2]
        effects = player.get("status_effects", [])
        player["status_effects"] = [e for e in effects if e["name"] != effect_name]
        save_state(state)
        ok(f"Removed effect: {effect_name}", player=name)
    else:
        error("Usage: effect <player> add/remove/list <name> [duration]")


def cmd_rest(args):
    """Rest at a safe location. Heals HP and clears temporary effects.
    Usage: rest [hours]  (default: 8 hours)
    """
    state = require_state()
    if not state:
        return

    hours = 8
    if args:
        hours = parse_int(args[0], "hours")
        if hours is None:
            return
        hours = max(1, min(hours, 24))

    results = {"ok": True, "hours": hours, "players": {}}

    for pname, player in state.get("players", {}).items():
        old_hp = player["hp"]
        healed = min(hours * 5, player["max_hp"] - player["hp"])
        player["hp"] = min(player["max_hp"], player["hp"] + healed)

        effects = player.get("status_effects", [])
        cleared = [e["name"] for e in effects if e.get("remaining", 0) != -1]
        player["status_effects"] = [e for e in effects if e.get("remaining", 0) == -1]

        results["players"][pname] = {
            "hp_restored": healed,
            "hp": f"{player['hp']}/{player['max_hp']}",
            "effects_cleared": cleared,
        }

    save_state(state)
    output(results, indent=True)


def cmd_recover(args):
    """Recover from state file backup.
    Usage: recover
    """
    backup = STATE_FILE + ".bak"
    if not os.path.exists(backup):
        return error("No backup file found")

    import shutil
    shutil.copy2(backup, STATE_FILE)

    from .util import load_state
    state = load_state()
    if state:
        ok("Restored from backup",
           turn=state.get("turn"),
           players=list(state.get("players", {}).keys()))
    else:
        error("Backup file is corrupted — manual intervention required")
