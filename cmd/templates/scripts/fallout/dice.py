"""Dice mechanics: rolls, skill checks, oracle, damage, initiative."""

import random
from .util import (
    error, ok, output, parse_int,
    require_state, require_player, validate_attr, validate_skill,
    roll_dice,
)


def cmd_roll(args):
    """Roll dice. Usage: roll <NdM> e.g. roll 2d20, roll 3d6"""
    if not args:
        return error("Usage: roll <NdM>, e.g. roll 2d20")

    expr = args[0].lower()
    try:
        count_s, sides_s = expr.split("d")
        count = int(count_s) if count_s else 1
        sides = int(sides_s)
    except (ValueError, IndexError):
        return error(f"Invalid dice expression: {expr}", hint="Format: NdM, e.g. 2d20, 3d6")

    if count < 1 or count > 100:
        return error(f"Dice count must be 1-100, got {count}")
    if sides < 1 or sides > 100:
        return error(f"Dice sides must be 1-100, got {sides}")

    results = roll_dice(count, sides)
    output({
        "ok": True,
        "dice": expr,
        "results": results,
        "total": sum(results),
        "min": min(results),
        "max": max(results),
    })


def _evaluate_2d20(player, attr, skill_name, difficulty, assist_bonus=0, helper_name=None):
    """Core 2d20 evaluation logic shared by check and assist-check.
    Returns result dict.
    """
    attr_val = player.get("special", {}).get(attr, 0)
    skill_val = player.get("skills", {}).get(skill_name, 0)
    is_tag = skill_name in player.get("tag_skills", [])
    target = attr_val + skill_val + assist_bonus

    dice = roll_dice(2, 20)
    successes = 0
    complications = 0
    crits = 0
    details = []

    for d in dice:
        if d == 1:
            successes += 2
            crits += 1
            details.append(f"{d} -> Critical Success (+2)")
        elif d == 20:
            complications += 1
            details.append(f"{d} -> Complication!")
        elif d <= target:
            successes += 1
            if is_tag and d <= skill_val:
                successes += 1
                crits += 1
                details.append(f"{d} -> Success + Tag Skill Crit (+1)")
            else:
                details.append(f"{d} -> Success")
        else:
            details.append(f"{d} -> Failure")

    passed = successes >= difficulty
    excess_ap = max(0, successes - difficulty) if passed else 0

    result = {
        "ok": True,
        "attribute": f"{attr} ({attr_val})",
        "skill": f"{skill_name} ({skill_val})" + (" [TAG]" if is_tag else ""),
        "target_number": target,
        "difficulty": difficulty,
        "dice": dice,
        "details": details,
        "successes": successes,
        "crits": crits,
        "complications": complications,
        "passed": passed,
        "excess_ap": excess_ap,
        "verdict": "Success!" if passed else "Failure...",
    }

    if assist_bonus > 0 and helper_name:
        result["helper"] = helper_name
        result["assist_bonus"] = f"+{assist_bonus} target number"

    if complications > 0:
        result["complication_note"] = "A complication occurred! Even on a success, trouble is brewing."

    return result, excess_ap


def cmd_check(args):
    """Skill check (2d20 system).
    Usage: check <player> <attribute> <skill> <difficulty>
    Example: check Jake PER Lockpick 2
    """
    if len(args) < 4:
        return error("Usage: check <player> <attribute> <skill> <difficulty>",
                      hint="Example: check Jake PER Lockpick 2")

    state = require_state()
    if not state:
        return

    player_name = args[0]
    player = require_player(state, player_name)
    if not player:
        return

    attr = validate_attr(args[1])
    if not attr:
        return

    skill_name = validate_skill(args[2])
    if not skill_name:
        return

    difficulty = parse_int(args[3], "difficulty")
    if difficulty is None:
        return

    if difficulty < 0:
        return error(f"Difficulty must be non-negative, got {difficulty}")

    result, excess_ap = _evaluate_2d20(player, attr, skill_name, difficulty)
    result["player"] = player_name

    if excess_ap > 0:
        from .util import save_state
        player["ap"] = player.get("ap", 0) + excess_ap
        save_state(state)

    output(result, indent=True)


def cmd_assist_check(args):
    """Assisted skill check.
    Usage: assist-check <player> <helper> <attribute> <skill> <difficulty>
    """
    if len(args) < 5:
        return error("Usage: assist-check <player> <helper> <attribute> <skill> <difficulty>",
                      hint="Example: assist-check Jake Sarah PER Lockpick 2")

    state = require_state()
    if not state:
        return

    player_name, helper_name = args[0], args[1]
    player = require_player(state, player_name)
    if not player:
        return
    helper = require_player(state, helper_name)
    if not helper:
        return

    attr = validate_attr(args[2])
    if not attr:
        return

    skill_name = validate_skill(args[3])
    if not skill_name:
        return

    difficulty = parse_int(args[4], "difficulty")
    if difficulty is None:
        return

    if difficulty < 0:
        return error(f"Difficulty must be non-negative, got {difficulty}")

    result, excess_ap = _evaluate_2d20(player, attr, skill_name, difficulty,
                                        assist_bonus=2, helper_name=helper_name)
    result["player"] = player_name

    if excess_ap > 0:
        from .util import save_state
        player["ap"] = player.get("ap", 0) + excess_ap
        save_state(state)

    output(result, indent=True)


def cmd_oracle(args):
    """Oracle D6 for narrative judgment."""
    result = random.randint(1, 6)
    meanings = {
        1: "No, and things get worse",
        2: "No",
        3: "No, but there's a silver lining",
        4: "Yes, but at a cost",
        5: "Yes",
        6: "Yes, and something extra",
    }
    output({"ok": True, "oracle_d6": result, "meaning": meanings[result]})


def cmd_damage(args):
    """Roll combat damage dice.
    Usage: damage <count> [bonus]
    Example: damage 3 2   (roll 3d6 combat dice with +2 bonus damage)
    """
    if not args:
        return error("Usage: damage <dice_count> [bonus]", hint="Example: damage 3 2")

    count = parse_int(args[0], "dice_count")
    if count is None:
        return
    if count < 1 or count > 20:
        return error(f"Dice count must be 1-20, got {count}")

    bonus = 0
    if len(args) > 1:
        bonus = parse_int(args[1], "bonus")
        if bonus is None:
            return

    dice = roll_dice(count, 6)
    total_damage = bonus
    effects = []
    details = []

    for d in dice:
        if d == 1:
            total_damage += 1
            details.append(f"{d} -> 1 damage")
        elif d == 2:
            total_damage += 2
            details.append(f"{d} -> 2 damage")
        elif d in (3, 4):
            details.append(f"{d} -> No damage")
        elif d in (5, 6):
            total_damage += 1
            effects.append("Special Effect")
            details.append(f"{d} -> 1 damage + Special Effect!")

    output({
        "ok": True,
        "dice": dice,
        "details": details,
        "base_damage": total_damage - bonus,
        "bonus": bonus,
        "total_damage": total_damage,
        "effects": effects,
    }, indent=True)


def cmd_initiative(args):
    """Calculate combat initiative order for all players."""
    state = require_state()
    if not state:
        return

    players = state.get("players", {})
    if not players:
        return error("No players in the game")

    order = []
    for name, player in players.items():
        per = player.get("special", {}).get("PER", 5)
        agi = player.get("special", {}).get("AGI", 5)
        init_val = per + agi
        order.append({"player": name, "character": player.get("character", name), "initiative": init_val})

    order.sort(key=lambda x: x["initiative"], reverse=True)
    output({"ok": True, "initiative_order": order}, indent=True)
