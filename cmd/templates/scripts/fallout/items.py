"""Consumables, status effects, rest, and recovery."""

import os
import random
from .util import error, ok, output, parse_int, require_state, require_player, save_state, STATE_FILE, register_action, get_mode, exit_combat
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

    inv = player.setdefault("inventory", {})
    if inv.get(item, 0) <= 0:
        return error(f"Player {name} does not have item: {item}",
                      inventory=inv,
                      hint=f"Add it first: inventory {name} add '{item}' 1")

    chem = CHEM_EFFECTS.get(item)
    if not chem:
        return error(f"Unknown consumable: {item}. This item cannot be used — it may be equipment or a crafting material.",
                      known_consumables=list(CHEM_EFFECTS.keys()))

    # Remove from inventory
    inv[item] -= 1
    if inv[item] <= 0:
        del inv[item]

    results = {"ok": True, "player": name, "item": item, "effects": []}

    if "heal" in chem:
        old_hp = player["hp"]
        heal_amount = chem["heal"]
        medicine_level = player.get("skills", {}).get("Medicine", 0)
        medicine_bonus = medicine_level * 2
        player["hp"] = min(player["max_hp"], player["hp"] + heal_amount + medicine_bonus)
        heal_desc = f"HP: {old_hp} -> {player['hp']}"
        if medicine_bonus > 0:
            heal_desc += f" (Medicine +{medicine_bonus})"
        results["effects"].append(heal_desc)

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
        if chem.get("stat_mods"):
            effect_entry["stat_mods"] = dict(chem["stat_mods"])
        if chem.get("damage_bonus"):
            effect_entry["damage_bonus"] = chem["damage_bonus"]
        if chem.get("damage_reduction"):
            effect_entry["damage_reduction"] = chem["damage_reduction"]
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

    # Register action for the player
    action_status = register_action(state, name)
    results["action_status"] = action_status

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
        return error("Usage: effect <player> add/remove/list <name> [duration]",
                      hint="Example: effect Jake add Inspired 3 | effect Jake remove Poisoned | effect Jake list")

    state = require_state()
    if not state:
        return

    # Legacy support: 'effect tick' redirects to a note
    if args[0] == "tick":
        return error("'effect tick' is deprecated. Use 'turn' command which automatically ticks effects.",
                      hint="Run: python3 scripts/fallout_game.py turn")

    if len(args) < 2:
        return error("Usage: effect <player> add/remove/list <name> [duration]",
                      hint="Example: effect Jake add Inspired 3 | effect Jake list")

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
        error("Usage: effect <player> add/remove/list <name> [duration]",
              hint="'add' needs: <name> <duration>. 'remove' needs: <name>. 'list' takes no extra args.")


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

    # Force exploration mode on rest
    transition = exit_combat(state)

    results = {"ok": True, "hours": hours, "players": {}}
    if transition:
        results.update(transition)
    results["mode"] = get_mode(state)

    # Clear all enemies on rest
    enemies = state.get("enemies", {})
    if enemies:
        results["enemies_cleared"] = list(enemies.keys())
        state["enemies"] = {}

    for pname, player in state.get("players", {}).items():
        old_hp = player["hp"]
        survival_level = player.get("skills", {}).get("Survival", 0)
        rate = 5 + survival_level  # base 5 HP/hour + 1 per Survival level
        healed = min(hours * rate, player["max_hp"] - player["hp"])
        player["hp"] = min(player["max_hp"], player["hp"] + healed)

        effects = player.get("status_effects", [])
        cleared = [e["name"] for e in effects if e.get("remaining", 0) != -1]
        player["status_effects"] = [e for e in effects if e.get("remaining", 0) == -1]

        player_result = {
            "hp_restored": healed,
            "hp": f"{player['hp']}/{player['max_hp']}",
            "effects_cleared": cleared,
        }
        if survival_level > 0:
            player_result["survival_bonus"] = f"+{survival_level} HP/hour"
        results["players"][pname] = player_result

    save_state(state)
    output(results, indent=True)


def cmd_recover(args):
    """Recover from state file backup.
    Usage: recover
    """
    backup = STATE_FILE + ".bak"
    if not os.path.exists(backup):
        return error("No backup file found",
                      hint="A backup (.bak) is created automatically on each save. If no save has occurred yet, there is nothing to recover.")

    import shutil
    shutil.copy2(backup, STATE_FILE)

    from .util import load_state
    state = load_state()
    if state:
        ok("Restored from backup",
           turn=state.get("turn"),
           players=list(state.get("players", {}).keys()))
    else:
        error("Backup file is corrupted — manual intervention required",
              hint="Check the .bak file manually, or run 'init' to start a new game.")
