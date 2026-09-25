#!/usr/bin/env python3
"""Exercise animated_oled_player.ino over a USB serial connection.

Requires pyserial: python3 -m pip install pyserial
Example: python3 test_oled_serial.py /dev/ttyACM0
"""

import argparse
import time

import serial


def main() -> None:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("port", help="Arduino USB serial port, e.g. /dev/ttyACM0")
    args = parser.parse_args()

    with serial.Serial(args.port, 115200, timeout=3) as board:
        time.sleep(2.5)  # Opening a classic Uno's serial port resets it.
        board.reset_input_buffer()

        def send(command: str, observe_seconds: float) -> None:
            payload = (command + "\n").encode("ascii")
            for offset in range(0, len(payload), 12):
                board.write(payload[offset : offset + 12])
                board.flush()
                time.sleep(0.04)
            reply = board.readline().decode("ascii", errors="replace").strip()
            print(f"{command.split(chr(9), 1)[0]:8} {len(payload):3} bytes -> {reply or 'NO REPLY'}")
            if reply != "OK":
                raise RuntimeError("Command failed; check U8G2_16BIT, port, and serial wiring")
            time.sleep(observe_seconds)

        # Small commands fit the Uno's RX buffer. END displays the full
        # track change at once; each command is acknowledged separately.
        send("PAGE\tPLAYER", 0)
        send("BEGIN", 0)
        send("TITLE\tA Very Long Song Title That Keeps Scrolling", 0)
        send("ARTIST\tFleetwood Mac and Friends Live Tonight", 0)
        send("ALBUM\tThe Complete Live Recordings Collection", 0)
        send("DURATION\t245", 0)
        send("TIME\t5", 0)
        send("STATE\tPLAYING", 0)
        send("END", 7)
        # Test each other field's scroll on its own, without title movement.
        send("TITLE\tDreams", 0)
        send("ARTIST\tFleetwood Mac and Friends Live Tonight", 6)
        send("ARTIST\tFleetwood Mac", 0)
        send("ALBUM\tThe Complete Live Recordings Collection", 6)
        send("STATE\tPAUSED", 2)
        send("TIME\t120", 2)
        send("STATE\tPLAYING", 3)
        send("STATE\tSTOPPED", 2)

        # Set the clock, then cycle through every screen.
        send("CLOCK\t12:34:56", 0)
        send("PAGE\tBOOT", 1)        # Boot animation, then NEXT before auto-exit
        send("NEXT", 1)               # Player
        send("NEXT", 1)               # Loading
        send("TEXT\tConnecting to MPD", 2)
        send("NEXT", 1)               # Error
        send("TEXT\tCannot reach MPD", 2)
        send("NEXT", 3)               # Clock: blink + seconds bar
        send("NEXT", 1)               # Message
        send("TEXT\tWelcome to the music room", 2)
        send("NEXT", 1)               # Back to boot animation

        # A page can also be selected directly with its custom message.
        send("PAGE\tLOADING\tScanning your music library", 2)
        send("PAGE\tERROR\tAudio output disconnected", 2)
        send("PAGE\tMESSAGE\tVolume set to 70 percent", 2)
        send("PAGE\tPLAYER", 1)


if __name__ == "__main__":
    main()
