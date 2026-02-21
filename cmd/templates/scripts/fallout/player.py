"""Player management: creation, removal, HP/rads/caps/AP, inventory, skills."""

from .util import (
    error, ok, output, parse_int,
    require_state, require_player, validate_skill, save_state,
    ALL_SKILLS, SPECIAL_ATTRS, RAD_PENALTIES,
)


def cmd_add_player(args):
    """Add a new player.
    Usage: add-player <name> <character> <background> <STR> <PER> <END> <CHA> <INT> <AGI> <LCK> <skill1> <skill2> <skill3>
    """
    if len(args) < 13:
        return error(
            "Usage: add-player <name> <character> <background> STR PER END CHA INT AGI LCK skill1 skill2 skill3",
            hint="Example: add-player Jake 'Vault Dweller' 'Tech Specialist' 4 7 5 4 8 6 6 Science Lockpick 'Small Guns'",
        )

    state = require_state()
    if not state:
        return

    name = args[0]
    character = args[1]
    background = args[2]

    # Parse and validate SPECIAL
    special = {}
    for i, attr in enumerate(SPECIAL_ATTRS):
        val = parse_int(args[3 + i], attr)
        if val is None:
            return
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
    for raw in args[10:13]:
        canonical = validate_skill(raw)
        if not canonical:
            return
        if canonical in tag_skills:
            return error(f"Duplicate tag skill: {canonical}", hint="Choose 3 different tag skills")
        tag_skills.append(canonical)

    hp = (special["END"] + special["LCK"]) * 5
    carry = 150 + special["STR"] * 10

    skills = {}
    for s in ALL_SKILLS:
        skills[s] = 2 if s in tag_skills else 0

    player = {
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
        "inventory": ["10mm Pistol", "10mm Ammo x24", "Stimpak", "Stimpak", "Purified Water", "Purified Water", "Purified Water"],
        "status_effects": [],
        "kills": 0,
        "quests_completed": 0,
    }

    state["players"][name] = player
    save_state(state)
    ok(f"Player {name} has joined the game", player=player, derived={"hp": hp, "carry_weight": carry})


def cmd_remove_player(args):
    """Remove a player. Usage: remove-player <name>"""
    if not args:
        return error("Usage: remove-player <name>")

    state = require_state()
    if not state:
        return

    name = " ".join(args)
    if name in state.get("players", {}):
        del state["players"][name]
        save_state(state)
        ok(f"Player {name} has been removed")
    else:
        available = list(state.get("players", {}).keys())
        error(f"Player not found: {name}", available_players=available)


def _modify_hp(args, negative):
    """Shared HP modification logic for hurt/heal."""
    action = "hurt" if negative else "heal"
    if len(args) < 2:
        return error(f"Usage: {action} <player> <amount>")

    state = require_state()
    if not state:
        return

    name = args[0]
    amount = parse_int(args[1], "amount")
    if amount is None:
        return

    player = require_player(state, name)
    if not player:
        return

    old_hp = player["hp"]
    if negative:
        player["hp"] = max(0, player["hp"] - amount)
    else:
        player["hp"] = min(player["max_hp"], player["hp"] + amount)

    save_state(state)
    status = "Down!" if player["hp"] <= 0 else "OK"
    output({
        "ok": True,
        "player": name,
        "action": "Damage" if negative else "Heal",
        "amount": amount,
        "hp_before": old_hp,
        "hp_after": player["hp"],
        "max_hp": player["max_hp"],
        "status": status,
    })


def cmd_hurt(args):
    """Deal damage to a player. Usage: hurt <player> <amount>"""
    _modify_hp(args, negative=True)


def cmd_heal(args):
    """Heal a player. Usage: heal <player> <amount>"""
    _modify_hp(args, negative=False)


def cmd_rads(args):
    """Modify radiation level. Usage: rads <player> <amount> (negative to reduce)"""
    if len(args) < 2:
        return error("Usage: rads <player> <amount>")

    state = require_state()
    if not state:
        return

    name = args[0]
    amount = parse_int(args[1], "amount")
    if amount is None:
        return

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
    """Modify caps. Usage: caps <player> <amount> (negative to spend)"""
    if len(args) < 2:
        return error("Usage: caps <player> <amount>")

    state = require_state()
    if not state:
        return

    name = args[0]
    amount = parse_int(args[1], "amount")
    if amount is None:
        return

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
    """Modify action points. Usage: ap <player> <amount>"""
    if len(args) < 2:
        return error("Usage: ap <player> <amount>")

    state = require_state()
    if not state:
        return

    name = args[0]
    amount = parse_int(args[1], "amount")
    if amount is None:
        return

    player = require_player(state, name)
    if not player:
        return

    old_ap = player.get("ap", 0)
    player["ap"] = max(0, old_ap + amount)
    save_state(state)
    output({
        "ok": True,
        "player": name,
        "ap_before": old_ap,
        "ap_after": player["ap"],
        "change": amount,
    })


def cmd_inventory(args):
    """Manage inventory. Usage: inventory <player> add/remove <item>"""
    if len(args) < 3:
        return error("Usage: inventory <player> add/remove <item>",
                      hint="Example: inventory Jake add Stimpak")

    state = require_state()
    if not state:
        return

    name = args[0]
    action = args[1].lower()
    item = " ".join(args[2:])

    player = require_player(state, name)
    if not player:
        return

    if action == "add":
        player.setdefault("inventory", []).append(item)
        save_state(state)
        ok(f"Added {item}", player=name, item=item, inventory=player["inventory"])
    elif action == "remove":
        inv = player.get("inventory", [])
        if item in inv:
            inv.remove(item)
            save_state(state)
            ok(f"Removed {item}", player=name, item=item, inventory=player["inventory"])
        else:
            error(f"Player {name} does not have item: {item}",
                  inventory=player.get("inventory", []))
    else:
        error("Action must be 'add' or 'remove'", hint="Usage: inventory <player> add/remove <item>")


def cmd_skill_up(args):
    """Increase a player's skill level.
    Usage: skill-up <player> <skill> [amount]
    """
    if len(args) < 2:
        return error("Usage: skill-up <player> <skill> [amount]",
                      hint=f"Valid skills: {', '.join(ALL_SKILLS)}")

    state = require_state()
    if not state:
        return

    name = args[0]
    player = require_player(state, name)
    if not player:
        return

    skill = validate_skill(args[1])
    if not skill:
        return

    amount = 1
    if len(args) > 2:
        amount = parse_int(args[2], "amount")
        if amount is None:
            return
        if amount < 1:
            return error("Amount must be positive")

    old = player.get("skills", {}).get(skill, 0)
    new = min(6, old + amount)
    player.setdefault("skills", {})[skill] = new
    save_state(state)
    output({
        "ok": True,
        "player": name,
        "skill": skill,
        "level_before": old,
        "level_after": new,
    })
