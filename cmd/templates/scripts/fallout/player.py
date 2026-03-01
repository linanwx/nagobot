"""Player management: creation, removal, HP/rads/caps/AP, inventory, skills."""

import re
from .util import (
    error, ok, output, parse_int,
    require_state, require_player, validate_skill, save_state,
    roll_dice, get_effective_special,
    ALL_SKILLS, SPECIAL_ATTRS, RAD_PENALTIES, clamp_ap,
)


def cmd_add_player(args):
    """Add a new player."""
    state = require_state()
    if not state:
        return

    player_id = args.player_id
    name = args.name
    character = args.character
    background = args.background

    # Validate SPECIAL
    special = {}
    for attr in SPECIAL_ATTRS:
        val = getattr(args, attr)
        if val < 1 or val > 10:
            return error(f"{attr} must be 1-10, got {val}", hint="Each SPECIAL attribute must be between 1 and 10")
        special[attr] = val

    total = sum(special.values())
    if total != 40:
        return error(
            f"SPECIAL total must be 40, got {total} (current: {special})",
            hint="Redistribute points so they sum to 40",
        )

    # Validate tag skills
    tag_skills = []
    for raw in [args.skill1, args.skill2, args.skill3]:
        canonical = validate_skill(raw)
        if not canonical:
            return
        if canonical in tag_skills:
            return error(f"Duplicate tag skill: {canonical}", hint="Choose 3 different tag skills")
        tag_skills.append(canonical)

    hp = special["END"] * 10
    carry = 150 + special["STR"] * 10

    skills = {}
    for s in ALL_SKILLS:
        skills[s] = 2 if s in tag_skills else 0

    player = {
        "player_id": player_id,
        "character": character,
        "background": background,
        "hp": hp,
        "max_hp": hp,
        "rads": 0,
        "caps": 100,
        "ap": 0,
        "carry_weight": carry,
        "special": special,
        "tag_skills": tag_skills,
        "skills": skills,
        "inventory": {"10mm Pistol": 1, "10mm Ammo": 24, "Stimpak": 2, "Purified Water": 3},
        "hunger": 0,
        "status_effects": [],
        "kills": 0,
        "quests_completed": 0,
    }

    state["players"][name] = player
    save_state(state)
    ok(f"Player {name} has joined the game", player=player, derived={"hp": hp, "carry_weight": carry},
       hint=f"Use 'inventory {name} add <item>' to customize starting gear. Call 'status {name}' to verify.")


def cmd_remove_player(args):
    """Remove a player."""
    state = require_state()
    if not state:
        return

    name = " ".join(args.name)
    if name in state.get("players", {}):
        del state["players"][name]
        save_state(state)
        ok(f"Player {name} has been removed")
    else:
        available = list(state.get("players", {}).keys())
        error(f"Player not found: {name}", available_players=available)


def _modify_hp(player_name, amount, negative):
    """Shared HP modification logic for hurt/heal."""
    state = require_state()
    if not state:
        return

    player = require_player(state, player_name)
    if not player:
        return

    # Medicine bonus: +2 HP per Medicine level when healing
    medicine_bonus = 0
    if not negative:
        medicine_level = player.get("skills", {}).get("Medicine", 0)
        medicine_bonus = medicine_level * 2
        amount += medicine_bonus

    # Damage reduction from status effects (e.g. Med-X)
    dmg_reduction = 0
    if negative:
        for eff in player.get("status_effects", []):
            dmg_reduction += eff.get("damage_reduction", 0)
        if dmg_reduction > 0:
            amount = max(1, amount - dmg_reduction)

    old_hp = player["hp"]
    if negative:
        player["hp"] = max(0, player["hp"] - amount)
    else:
        player["hp"] = min(player["max_hp"], player["hp"] + amount)

    effects = player.get("status_effects", [])
    has_incap = any(e["name"] == "Incapacitated" for e in effects)

    # Auto-add Incapacitated on HP reaching 0
    if player["hp"] <= 0 and old_hp > 0 and not has_incap:
        effects.append({"name": "Incapacitated", "remaining": 3})

    # Auto-remove Incapacitated on healing above 0
    if player["hp"] > 0 and has_incap:
        player["status_effects"] = [e for e in effects if e["name"] != "Incapacitated"]

    save_state(state)
    status = "Down! (Incapacitated — 3 turns to stabilize)" if player["hp"] <= 0 else "OK"
    result = {
        "ok": True,
        "player": player_name,
        "action": "Damage" if negative else "Heal",
        "amount": amount,
        "hp_before": old_hp,
        "hp_after": player["hp"],
        "max_hp": player["max_hp"],
        "status": status,
    }
    if player["hp"] <= 0 and negative:
        result["hint"] = f"{player_name} is down! Allies must stabilize within 3 turns (Medicine check or heal above 0 HP), or {player_name} dies."
    if medicine_bonus > 0:
        result["medicine_bonus"] = medicine_bonus
    if dmg_reduction > 0:
        result["damage_reduction"] = dmg_reduction
    output(result)


def cmd_hurt(args):
    """Deal damage to a player."""
    _modify_hp(args.player, args.amount, negative=True)


def cmd_heal(args):
    """Heal a player."""
    _modify_hp(args.player, args.amount, negative=False)


def cmd_rads(args):
    """Modify radiation level."""
    state = require_state()
    if not state:
        return

    name = args.player
    amount = args.amount

    player = require_player(state, name)
    if not player:
        return

    old_rads = player.get("rads", 0)
    player["rads"] = max(0, old_rads + amount)
    save_state(state)

    # Generate radiation effect text dynamically from RAD_PENALTIES
    rad_effects = []
    r = player["rads"]
    severity_names = ["Lethal", "Critical", "Severe", "Moderate", "Minor"]
    for i, (threshold, penalties) in enumerate(RAD_PENALTIES):
        if r >= threshold:
            label = severity_names[i] if i < len(severity_names) else f"{threshold}+"
            mods = ", ".join(f"{a}{v:+d}" for a, v in penalties.items())
            rad_effects.append(f"{label} radiation: {mods}")
            break

    output({
        "ok": True,
        "player": name,
        "rads_before": old_rads,
        "rads_after": player["rads"],
        "change": amount,
        "effects": rad_effects,
    })


def cmd_caps(args):
    """Modify caps."""
    state = require_state()
    if not state:
        return

    name = args.player
    amount = args.amount

    player = require_player(state, name)
    if not player:
        return

    old_caps = player.get("caps", 0)
    player["caps"] = max(0, old_caps + amount)
    save_state(state)
    output({
        "ok": True,
        "player": name,
        "caps_before": old_caps,
        "caps_after": player["caps"],
        "change": amount,
    })


def cmd_ap(args):
    """Modify action points."""
    state = require_state()
    if not state:
        return

    name = args.player
    amount = args.amount

    player = require_player(state, name)
    if not player:
        return

    old_ap = player.get("ap", 0)
    player["ap"] = max(0, old_ap + amount)
    clamp_ap(player)
    save_state(state)
    output({
        "ok": True,
        "player": name,
        "ap_before": old_ap,
        "ap_after": player["ap"],
        "change": amount,
    })


def cmd_inventory(args):
    """Manage inventory."""
    state = require_state()
    if not state:
        return

    name = args.player
    action = args.action.lower()

    player = require_player(state, name)
    if not player:
        return

    inv = player.setdefault("inventory", {})

    item = " ".join(args.item)
    qty = args.qty

    # Handle "Item xN" suffix in item name
    match = re.match(r'^(.+?)\s+x(\d+)$', item)
    if match:
        item = match.group(1)
        qty *= int(match.group(2))

    if action == "add":
        inv[item] = inv.get(item, 0) + qty
        save_state(state)
        ok(f"Added {item} x{qty}", player=name, item=item, qty=inv[item], inventory=inv)
    elif action == "remove":
        current = inv.get(item, 0)
        if current <= 0:
            error(f"Player {name} does not have item: {item}", inventory=inv)
        elif qty >= current:
            del inv[item]
            save_state(state)
            ok(f"Removed {item} x{current}", player=name, item=item, qty=0, inventory=inv)
        else:
            inv[item] = current - qty
            save_state(state)
            ok(f"Removed {item} x{qty}", player=name, item=item, qty=inv[item], inventory=inv)
    else:
        error("Action must be 'add' or 'remove'",
              hint="Example: inventory Jake add Stimpak --qty 3 | inventory Jake remove 'Fusion Cell' --qty 10")


def cmd_skill_up(args):
    """Increase a player's skill level."""
    state = require_state()
    if not state:
        return

    name = args.player
    player = require_player(state, name)
    if not player:
        return

    skill = validate_skill(args.skill)
    if not skill:
        return

    amount = args.amount
    if amount < 1:
        return error("Amount must be positive", hint="Example: skill-up Jake Lockpick --amount 1")

    old = player.get("skills", {}).get(skill, 0)
    new = min(6, old + amount)

    # INT check: 2d20 vs effective INT, difficulty 2 — bonus +1 on success
    effective, _ = get_effective_special(player)
    int_val = effective.get("INT", 5)
    int_dice = roll_dice(2, 20)
    int_successes = 0
    int_details = []
    for d in int_dice:
        if d == 1:
            int_successes += 2
            int_details.append(f"{d} -> Critical (+2)")
        elif d <= int_val:
            int_successes += 1
            int_details.append(f"{d} -> Success")
        else:
            int_details.append(f"{d} -> Failure")
    int_triggered = int_successes >= 2
    if int_triggered:
        new = min(6, new + 1)

    player.setdefault("skills", {})[skill] = new
    save_state(state)
    result = {
        "ok": True,
        "player": name,
        "skill": skill,
        "level_before": old,
        "level_after": new,
        "int_check": {
            "target": int_val,
            "dice": int_dice,
            "details": int_details,
            "successes": int_successes,
            "needed": 2,
            "triggered": int_triggered,
        },
    }
    if int_triggered:
        result["int_bonus"] = True
        result["int_message"] = f"Smart! {name} gains an extra skill point from high Intelligence."
    output(result, indent=True)
