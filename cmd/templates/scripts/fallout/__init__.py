"""Fallout Wasteland RPG — Game Engine Package."""

import argparse
import json
import sys

from .dice import cmd_roll, cmd_check, cmd_damage, cmd_initiative
from .player import cmd_add_player, cmd_remove_player, cmd_hurt, cmd_heal, cmd_rads, cmd_caps, cmd_ap, cmd_inventory, cmd_skill_up
from .world import cmd_init, cmd_status, cmd_set, cmd_turn, cmd_location, cmd_move_team
from .items import cmd_use_item, cmd_effect, cmd_rest, cmd_recover
from .events import cmd_loot, cmd_trade, cmd_npc_gen
from .enemy import cmd_enemy_add, cmd_enemy_hurt, cmd_enemy_attack
from .format import cmd_format_response


class GameParser(argparse.ArgumentParser):
    """ArgumentParser that outputs JSON errors instead of stderr."""

    def error(self, message):
        print(json.dumps({"error": message}))
        sys.exit(1)


def _sub(subparsers, name, func, **kwargs):
    """Helper to add a subparser with GameParser class."""
    p = subparsers.add_parser(name, **kwargs)
    p.__class__ = GameParser
    p.set_defaults(func=func)
    return p


def build_parser():
    parser = GameParser(prog="fallout_game.py")
    sub = parser.add_subparsers(dest="command")

    # -- World --
    _sub(sub, "init", cmd_init)

    p = _sub(sub, "status", cmd_status)
    p.add_argument("player", nargs="?", default=None)

    p = _sub(sub, "set", cmd_set)
    p.add_argument("field")
    p.add_argument("value", nargs="+")

    _sub(sub, "turn", cmd_turn)

    p = _sub(sub, "location", cmd_location)
    p.add_argument("player", nargs="?", default=None)
    p.add_argument("new_location", nargs="*")

    p = _sub(sub, "move-team", cmd_move_team)
    p.add_argument("location", nargs="+")

    # -- Player --
    p = _sub(sub, "add-player", cmd_add_player)
    p.add_argument("player_id")
    p.add_argument("name")
    p.add_argument("character")
    p.add_argument("background")
    p.add_argument("STR", type=int)
    p.add_argument("PER", type=int)
    p.add_argument("END", type=int)
    p.add_argument("CHA", type=int)
    p.add_argument("INT", type=int)
    p.add_argument("AGI", type=int)
    p.add_argument("LCK", type=int)
    p.add_argument("skill1")
    p.add_argument("skill2")
    p.add_argument("skill3")

    p = _sub(sub, "remove-player", cmd_remove_player)
    p.add_argument("name", nargs="+")

    p = _sub(sub, "hurt", cmd_hurt)
    p.add_argument("player")
    p.add_argument("amount", type=int)

    p = _sub(sub, "heal", cmd_heal)
    p.add_argument("player")
    p.add_argument("amount", type=int)

    p = _sub(sub, "rads", cmd_rads)
    p.add_argument("player")
    p.add_argument("amount", type=int)

    p = _sub(sub, "caps", cmd_caps)
    p.add_argument("player")
    p.add_argument("amount", type=int)

    p = _sub(sub, "ap", cmd_ap)
    p.add_argument("player")
    p.add_argument("amount", type=int)

    p = _sub(sub, "inventory", cmd_inventory)
    p.add_argument("player")
    p.add_argument("action")
    p.add_argument("item", nargs="+")
    p.add_argument("--qty", type=int, default=1)

    p = _sub(sub, "skill-up", cmd_skill_up)
    p.add_argument("player")
    p.add_argument("skill")
    p.add_argument("--amount", type=int, default=1)

    # -- Dice --
    p = _sub(sub, "roll", cmd_roll)
    p.add_argument("expr")

    p = _sub(sub, "check", cmd_check)
    p.add_argument("players")
    p.add_argument("attr")
    p.add_argument("skill")
    p.add_argument("difficulty", type=int)
    p.add_argument("--ap", type=int, default=0)
    p.add_argument("--bonus", type=int, default=0)

    p = _sub(sub, "damage", cmd_damage)
    p.add_argument("player")
    p.add_argument("weapon", nargs="+")
    p.add_argument("--ap", type=int, default=0)

    _sub(sub, "initiative", cmd_initiative)

    # -- Items --
    p = _sub(sub, "use-item", cmd_use_item)
    p.add_argument("player")
    p.add_argument("item", nargs="+")
    p.add_argument("--provider", default=None, help="Who provides the item (default: player)")
    p.add_argument("--target", default=None, help="Who receives the effect (default: player)")

    p = _sub(sub, "effect", cmd_effect)
    p.add_argument("player")
    p.add_argument("action")
    p.add_argument("name", nargs="?", default=None)
    p.add_argument("--duration", type=int, default=None)

    p = _sub(sub, "rest", cmd_rest)
    p.add_argument("--hours", type=int, default=8)

    _sub(sub, "recover", cmd_recover)

    # -- Events --
    p = _sub(sub, "loot", cmd_loot)
    p.add_argument("tier", nargs="?", default=None)
    p.add_argument("--count", type=int, default=None)
    p.add_argument("--random-tier", action="store_true", default=False)

    p = _sub(sub, "trade", cmd_trade)
    p.add_argument("player")
    p.add_argument("base_price", type=int)
    p.add_argument("action")

    p = _sub(sub, "npc-gen", cmd_npc_gen)
    p.add_argument("--count", type=int, default=1)

    # -- Enemy --
    p = _sub(sub, "enemy-add", cmd_enemy_add)
    p.add_argument("args", nargs="+")

    p = _sub(sub, "enemy-hurt", cmd_enemy_hurt)
    p.add_argument("name")
    p.add_argument("amount", type=int)

    p = _sub(sub, "enemy-attack", cmd_enemy_attack)
    p.add_argument("enemy")
    p.add_argument("target", nargs="?", default="")
    p.add_argument("--random", action="store_true", help="Randomly select a living player as target")

    # -- Format --
    p = _sub(sub, "format-response", cmd_format_response)
    p.add_argument("--checks", default="", help="Comma-separated check IDs, e.g. '1,2'")
    p.add_argument("--damages", default="", help="Comma-separated damage IDs, e.g. '1,2'")
    p.add_argument("--attacks", default="", help="Comma-separated enemy attack IDs, e.g. '1,2'")
    p.add_argument("--summary", default="", help="Brief narrative summary / scene description hint")
    p.add_argument("--options", default="", help="Per-player options as XML, e.g. '<Name>opt</Name>'")

    return parser
