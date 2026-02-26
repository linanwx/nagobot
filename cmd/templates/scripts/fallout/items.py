"""Consumables, status effects, rest, and recovery."""

import os
import random
from .util import error, ok, output, parse_int, require_state, require_player, save_state, STATE_FILE, register_action, get_mode, exit_combat
from .data import CHEM_EFFECTS


def cmd_use_item(args):
    """Use a consumable item. Player performs the action, provider supplies the item, target receives the effect."""
    state = require_state()
    if not state:
        return

    player_name = args.player
    provider_name = args.provider or player_name
    target_name = args.target or player_name
    item = " ".join(args.item)

    # Validate all three roles
    player = require_player(state, player_name)
    if not player:
        return
    provider = require_player(state, provider_name)
    if not provider:
        return
    target = require_player(state, target_name)
    if not target:
        return

    # Check provider's inventory
    inv = provider.setdefault("inventory", {})
    if inv.get(item, 0) <= 0:
        return error(f"Player {provider_name} does not have item: {item}",
                      inventory=inv,
                      hint=f"Add it first: inventory {provider_name} add '{item}'")

    chem = CHEM_EFFECTS.get(item)
    if not chem:
        return error(f"Unknown consumable: {item}. This item cannot be used — it may be equipment or a crafting material.",
                      known_consumables=list(CHEM_EFFECTS.keys()))

    # Remove from provider's inventory
    inv[item] -= 1
    if inv[item] <= 0:
        del inv[item]

    results = {"ok": True, "player": player_name, "provider": provider_name,
               "target": target_name, "item": item, "effects": []}

    # Heal: Medicine bonus from player (performer), HP restored on target
    if "heal" in chem:
        old_hp = target["hp"]
        heal_amount = chem["heal"]
        medicine_level = player.get("skills", {}).get("Medicine", 0)
        medicine_bonus = medicine_level * 2
        target["hp"] = min(target["max_hp"], target["hp"] + heal_amount + medicine_bonus)
        heal_desc = f"{target_name} HP: {old_hp} -> {target['hp']}"
        if medicine_bonus > 0:
            heal_desc += f" ({player_name} Medicine +{medicine_bonus})"
        results["effects"].append(heal_desc)

    # Rads: applied to target
    if "rads" in chem:
        old_rads = target.get("rads", 0)
        target["rads"] = max(0, old_rads + chem["rads"])
        results["effects"].append(f"{target_name} Rads: {old_rads} -> {target['rads']}")

    # AP: applied to target
    if "ap" in chem:
        old_ap = target.get("ap", 0)
        target["ap"] = old_ap + chem["ap"]
        results["effects"].append(f"{target_name} AP: {old_ap} -> {target['ap']}")

    # Status effect: applied to target
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
        target.setdefault("status_effects", []).append(effect_entry)
        results["effects"].append(f"{target_name} Status gained: {chem['effect']} ({chem.get('duration', 1)} rounds)")

        # Addiction check: target is the one who might get addicted
        if "Addiction risk" in chem.get("desc", ""):
            addiction_roll = random.randint(1, 20)
            if addiction_roll <= 3:
                target.setdefault("status_effects", []).append({
                    "name": f"{item} Addiction",
                    "remaining": -1,
                    "source": "addiction",
                })
                results["effects"].append(f"WARNING: {target_name} Addicted! (Roll: {addiction_roll}, needed >3)")
                results["addicted"] = True
                results["addiction_hint"] = f"{item} Addiction is permanent until cured. Treatment: Medicine check (difficulty 3) or Addictol."
            else:
                results["effects"].append(f"{target_name} Not addicted (Roll: {addiction_roll}, needed <=3)")

    results["description"] = chem["desc"]

    # Register action for the player (performer)
    action_status = register_action(state, player_name)
    results["action_status"] = action_status

    save_state(state)
    output(results, indent=True)


def cmd_effect(args):
    """Manage status effects."""
    state = require_state()
    if not state:
        return

    name = args.player
    action = args.action.lower()

    player = require_player(state, name)
    if not player:
        return

    if action == "list":
        effects = player.get("status_effects", [])
        output({"ok": True, "player": name, "effects": effects}, indent=True)
    elif action == "add":
        if not args.name:
            return error("Usage: effect <player> add <name> --duration <N>",
                          hint="Example: effect Jake add Inspired --duration 3")
        duration = args.duration
        if duration is None:
            return error("Duration required for add: effect <player> add <name> --duration <N>",
                          hint="Example: effect Jake add Inspired --duration 3")
        player.setdefault("status_effects", []).append({
            "name": args.name,
            "remaining": duration,
            "source": "gm",
        })
        save_state(state)
        ok(f"Added effect: {args.name} ({duration} turns)", player=name)
    elif action == "remove":
        if not args.name:
            return error("Usage: effect <player> remove <name>",
                          hint="Example: effect Jake remove Poisoned")
        effect_name = args.name
        effects = player.get("status_effects", [])
        player["status_effects"] = [e for e in effects if e["name"] != effect_name]
        save_state(state)
        ok(f"Removed effect: {effect_name}", player=name)
    else:
        error("Action must be 'add', 'remove', or 'list'",
              hint="Example: effect Jake add Inspired --duration 3 | effect Jake remove Poisoned | effect Jake list")


def cmd_rest(args):
    """Rest at a safe location. Heals HP and clears temporary effects."""
    state = require_state()
    if not state:
        return

    hours = max(1, min(args.hours, 24))

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

    results["hint"] = "Rest complete. Call 'turn' to advance time."

    save_state(state)
    output(results, indent=True)


def cmd_recover(args):
    """Recover from state file backup."""
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
