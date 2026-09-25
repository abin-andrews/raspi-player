#include <Arduino.h>
#include <U8g2lib.h>
#include <SPI.h>
#include <string.h>

// Keep protocol keywords in flash on an AVR instead of copying them to SRAM.
#if defined(ARDUINO_ARCH_AVR)
#include <avr/pgmspace.h>
#define CMD_EQ(text, keyword) (strcmp_P((text), PSTR(keyword)) == 0)
#else
#define CMD_EQ(text, keyword) (strcmp((text), (keyword)) == 0)
#endif

// For a 256-pixel-wide SSD1322 on an AVR, enable U8G2_16BIT in the U8g2
// library's src/clib/u8g2.h and rebuild the library. Defining it only here
// would disagree with U8g2's separately compiled implementation.
#if defined(ARDUINO_ARCH_AVR) && !defined(U8G2_16BIT)
#error "SSD1322 256x64 requires U8G2_16BIT enabled in U8g2/src/clib/u8g2.h"
#endif

// Same display and pins as your working sketch: CS=10, DC=9, RESET=8.
U8G2_SSD1322_NHD_256X64_1_4W_HW_SPI u8g2(U8G2_R0, 10, 9, 8);

// Send UTF-8 metadata only if your chosen U8g2 font supports its glyphs.
// The included fonts and pixel-by-pixel scroll target ASCII text.
// The host should replace tabs/newlines in metadata with spaces.
//
// 115200 baud, one newline-terminated command per line:
// BEGIN / END  (batch track updates so the screen changes only after END)
// TIME<TAB>elapsed
// DURATION<TAB>duration
// STATE<TAB>PLAYING|PAUSED|STOPPED
// TITLE<TAB>title       ARTIST<TAB>artist       ALBUM<TAB>album
// PAGE<TAB>BOOT|PLAYER|LOADING|ERROR|CLOCK|MESSAGE[<TAB>message]
// NEXT  (cycle pages), PREV (cycle backwards), TEXT<TAB>message
// CLOCK<TAB>HH:MM:SS  (sets clock; PAGE<TAB>CLOCK shows it)
// Replies: OK for an accepted line, ERR for a bad or oversized line.
//
// Example batch: BEGIN\nTITLE\tDreams\nARTIST\tFleetwood Mac\n
//                ALBUM\tRumours\nDURATION\t258\nTIME\t83\nSTATE\tPLAYING\nEND\n
namespace {
// Each command is at most 63 bytes including the newline. Metadata fields
// remain 47/39/39 bytes; update them separately inside BEGIN/END.
constexpr uint8_t TITLE_CAP = 48;
constexpr uint8_t TEXT_CAP = 40;
constexpr uint8_t RX_CAP = 64;
constexpr uint32_t MAX_SECONDS = 604800UL; // one week
constexpr uint16_t FRAME_MS = 60;           // cap redraws at about 16/sec

char title[TITLE_CAP];
char artist[TEXT_CAP];
char album[TEXT_CAP];
char rxLine[RX_CAP];
uint8_t rxLength = 0;
bool discardingLine = false;
bool lineReady = false;
bool inBatch = false;
uint32_t batchStartMs = 0;

enum PlayerState : uint8_t { PLAYING, PAUSED, STOPPED };
PlayerState state = PLAYING;
uint32_t durationSeconds = 258;
uint32_t positionSeconds = 83;
uint32_t positionTickMs = 0;
uint32_t lastFrameMs = 0;
bool dirty = true;

struct Marquee {
  uint8_t start = 0;
  uint8_t pixel = 0; // offset into the first visible glyph
  bool atEnd = false;
  uint32_t nextStepMs = 0;
};
Marquee titleScroll, artistScroll, albumScroll;
Marquee pageScroll;

enum Page : uint8_t {
  PAGE_BOOT, PAGE_PLAYER, PAGE_LOADING, PAGE_ERROR, PAGE_CLOCK, PAGE_MESSAGE,
  PAGE_COUNT
};
Page page = PAGE_BOOT;
char pageText[TITLE_CAP]; // reused by loading, error, message
uint32_t pageStartMs = 0;
uint8_t lastAnimationPhase = 255;
uint8_t clockHour = 0, clockMinute = 0, clockSecond = 0;
uint32_t clockTickMs = 0;
bool clockIsSet = false;

void copyText(char *dest, size_t capacity, const char *source) {
  if (capacity == 0) return;
  strncpy(dest, source, capacity - 1);
  dest[capacity - 1] = '\0';
}

#if defined(ARDUINO_ARCH_AVR)
#define COPY_DEFAULT(dest, literal) strcpy_P((dest), PSTR(literal))
#else
#define COPY_DEFAULT(dest, literal) copyText((dest), sizeof(dest), (literal))
#endif

void resetScroll(Marquee &scroll, uint32_t now) {
  scroll.start = 0;
  scroll.pixel = 0;
  scroll.atEnd = false;
  scroll.nextStepMs = now + 1600; // let the beginning be read first
}

void showPage(Page next, const char *message, uint32_t now) {
  page = next;
  pageStartMs = now;
  lastAnimationPhase = 255;
  if (message) copyText(pageText, sizeof(pageText), message);
  else if (next == PAGE_LOADING) COPY_DEFAULT(pageText, "Loading music");
  else if (next == PAGE_ERROR) COPY_DEFAULT(pageText, "Playback unavailable");
  else if (next == PAGE_MESSAGE) COPY_DEFAULT(pageText, "Hello!");
  resetScroll(pageScroll, now);
  if (next == PAGE_PLAYER) {
    resetScroll(titleScroll, now);
    resetScroll(artistScroll, now);
    resetScroll(albumScroll, now);
  }
  dirty = true;
}

bool parseClock(const char *s, uint8_t &hour, uint8_t &minute, uint8_t &second) {
  if (strlen(s) != 8 || s[2] != ':' || s[5] != ':') return false;
  const uint8_t positions[] = {0, 1, 3, 4, 6, 7};
  for (uint8_t i = 0; i < 6; ++i)
    if (s[positions[i]] < '0' || s[positions[i]] > '9') return false;
  hour = (s[0] - '0') * 10 + s[1] - '0';
  minute = (s[3] - '0') * 10 + s[4] - '0';
  second = (s[6] - '0') * 10 + s[7] - '0';
  return hour < 24 && minute < 60 && second < 60;
}

bool parseSeconds(const char *s, uint32_t &out) {
  if (*s == '\0') return false;
  uint32_t number = 0;
  while (*s) {
    if (*s < '0' || *s > '9') return false;
    const uint8_t digit = *s++ - '0';
    if (number > (MAX_SECONDS - digit) / 10) return false;
    number = number * 10 + digit;
  }
  out = number;
  return true;
}

bool parseState(const char *s, PlayerState &out) {
  if (CMD_EQ(s, "PLAYING")) out = PLAYING;
  else if (CMD_EQ(s, "PAUSED")) out = PAUSED;
  else if (CMD_EQ(s, "STOPPED")) out = STOPPED;
  else return false;
  return true;
}

bool handleCommand(char *line) {
  const uint32_t now = millis();
  if (CMD_EQ(line, "BEGIN")) {
    inBatch = true;
    batchStartMs = now;
    return true;
  }
  if (CMD_EQ(line, "END")) {
    if (!inBatch) return false;
    inBatch = false;
    dirty = true;
    return true;
  }
  if (CMD_EQ(line, "NEXT") || CMD_EQ(line, "PREV")) {
    const uint8_t next = CMD_EQ(line, "NEXT") ?
      (page + 1) % PAGE_COUNT : (page + PAGE_COUNT - 1) % PAGE_COUNT;
    showPage(static_cast<Page>(next), nullptr, now);
    return true;
  }
  char *tab = strchr(line, '\t');
  if (!tab) return false;
  *tab++ = '\0';

  if (CMD_EQ(line, "PAGE")) {
    char *message = strchr(tab, '\t');
    if (message) *message++ = '\0';
    Page next;
    if (CMD_EQ(tab, "BOOT")) next = PAGE_BOOT;
    else if (CMD_EQ(tab, "PLAYER")) next = PAGE_PLAYER;
    else if (CMD_EQ(tab, "LOADING")) next = PAGE_LOADING;
    else if (CMD_EQ(tab, "ERROR")) next = PAGE_ERROR;
    else if (CMD_EQ(tab, "CLOCK")) next = PAGE_CLOCK;
    else if (CMD_EQ(tab, "MESSAGE")) next = PAGE_MESSAGE;
    else return false;
    showPage(next, message, now);
  } else if (CMD_EQ(line, "TEXT")) {
    if (page != PAGE_LOADING && page != PAGE_ERROR && page != PAGE_MESSAGE) return false;
    copyText(pageText, sizeof(pageText), tab);
    resetScroll(pageScroll, now);
  } else if (CMD_EQ(line, "CLOCK")) {
    uint8_t hour, minute, second;
    if (!parseClock(tab, hour, minute, second)) return false;
    clockHour = hour;
    clockMinute = minute;
    clockSecond = second;
    clockTickMs = now;
    clockIsSet = true;
  } else if (CMD_EQ(line, "TIME")) {
    uint32_t elapsed;
    if (!parseSeconds(tab, elapsed)) return false;
    positionSeconds = durationSeconds > 0 && elapsed > durationSeconds
      ? durationSeconds : elapsed;
    positionTickMs = now; // authoritative position; extrapolate from here
  } else if (CMD_EQ(line, "DURATION")) {
    uint32_t total;
    if (!parseSeconds(tab, total)) return false;
    durationSeconds = total;
    if (total > 0 && positionSeconds > total) positionSeconds = total;
  } else if (CMD_EQ(line, "STATE")) {
    PlayerState newState;
    if (!parseState(tab, newState)) return false;
    state = newState;
    if (state == STOPPED) positionSeconds = 0;
    positionTickMs = now;
  } else if (CMD_EQ(line, "TITLE")) {
    copyText(title, sizeof(title), tab);
    resetScroll(titleScroll, now);
  } else if (CMD_EQ(line, "ARTIST")) {
    copyText(artist, sizeof(artist), tab);
    resetScroll(artistScroll, now);
  } else if (CMD_EQ(line, "ALBUM")) {
    copyText(album, sizeof(album), tab);
    resetScroll(albumScroll, now);
  } else {
    return false;
  }
  if (!inBatch) dirty = true;
  return true;
}

// Only assemble bytes here. Calling this between U8g2 pages prevents the
// Uno's smaller hardware RX buffer from dropping an arriving command.
// Never apply a command here: the display must keep one frame snapshot.
void collectSerial() {
  if (lineReady) return;
  while (Serial.available()) {
    const char c = static_cast<char>(Serial.read());
    if (c == '\r') continue;
    if (c == '\n') {
      if (!discardingLine && rxLength > 0) {
        rxLine[rxLength] = '\0';
        lineReady = true;
        return;
      }
      if (discardingLine) {
        rxLine[0] = '\0';
        lineReady = true; // let processSerial() report ERR
        return;
      }
      rxLength = 0;
      discardingLine = false;
    } else if (!discardingLine) {
      if (rxLength < RX_CAP - 1) rxLine[rxLength++] = c;
      else discardingLine = true; // discard the whole oversized command
    }
  }
}

void processSerial() {
  collectSerial();
  while (lineReady) {
    const bool accepted = handleCommand(rxLine);
    Serial.println(accepted ? F("OK") : F("ERR"));
    lineReady = false;
    rxLength = 0;
    discardingLine = false;
    collectSerial();
  }
}

bool advanceScroll(Marquee &scroll, const char *value, uint16_t width,
                   const uint8_t *font, uint32_t now) {
  if (static_cast<int32_t>(now - scroll.nextStepMs) < 0) return false;
  u8g2.setFont(font);
  if (u8g2.getStrWidth(value) <= width) {
    scroll.nextStepMs = now + 1000;
    return false;
  }

  // The title has 13 spare pixels on its left so partial glyphs can be
  // clipped without ever supplying a negative x coordinate to U8g2.
  const uint16_t available = width - (font == u8g2_font_helvB14_tr ? 13 : 0);
  if (u8g2.getStrWidth(value + scroll.start) <= available + scroll.pixel) {
    if (!scroll.atEnd) {
      scroll.atEnd = true;
      scroll.nextStepMs = now + 1000; // pause on the final words
      return false;
    }
    resetScroll(scroll, now);
    return true;
  }

  const char glyph[2] = { value[scroll.start], '\0' };
  const uint8_t glyphWidth = u8g2.getStrWidth(glyph);
  ++scroll.pixel;
  if (scroll.pixel >= (glyphWidth ? glyphWidth : 1)) {
    scroll.pixel = 0;
    ++scroll.start;
  }
  scroll.nextStepMs = now + FRAME_MS;
  return true;
}

// Calculate the row once before the page loop, avoiding repeated width
// measurement for each SSD1322 page. This uses a few stack bytes, not a bitmap.
struct RowView {
  const char *visible;
  uint16_t textX;
  uint16_t clipX;
  uint16_t clipEnd;
};

RowView layoutRow(const char *value, const Marquee &scroll,
                  uint16_t x, uint16_t width, uint8_t leftInset) {
  const bool overflow = u8g2.getStrWidth(value) > width;
  const uint16_t lead = overflow ? leftInset : 0;
  const char *visible = value + scroll.start;
  RowView view = {
    visible,
    static_cast<uint16_t>(x + lead - scroll.pixel),
    static_cast<uint16_t>(x + lead),
    static_cast<uint16_t>(x + width)
  };
  return view;
}

void drawScrollingRow(const RowView &view, uint8_t baseline,
                      uint8_t top, uint8_t bottom) {
  u8g2.setClipWindow(view.clipX, top, view.clipEnd, bottom);
  u8g2.drawStr(view.textX, baseline, view.visible);
  u8g2.setMaxClipWindow();
}

void formatTime(char *out, uint32_t seconds) {
  // MAX_SECONDS is one week, so minutes need at most five digits.
  uint16_t minutes = seconds / 60;
  uint8_t index = 0;
  uint16_t divisor = 10000;
  bool started = false;
  while (divisor > 0) {
    const uint8_t digit = minutes / divisor;
    minutes %= divisor;
    if (digit || started || divisor <= 10) {
      out[index++] = '0' + digit;
      started = true;
    }
    divisor /= 10;
  }
  out[index++] = ':';
  const uint8_t remainder = seconds % 60;
  out[index++] = '0' + remainder / 10;
  out[index++] = '0' + remainder % 10;
  out[index] = '\0';
}

void drawScreen() {
  const uint32_t frameNow = millis();
  char elapsed[10], total[10];
  RowView titleView = {}, artistView = {}, albumView = {}, pageView = {};

  if (page == PAGE_PLAYER) {
    formatTime(elapsed, positionSeconds);
    if (durationSeconds == 0) COPY_DEFAULT(total, "--:--");
    else formatTime(total, durationSeconds);
    u8g2.setFont(u8g2_font_helvB14_tr);
    titleView = layoutRow(title, titleScroll, 7, 242, 13);
    u8g2.setFont(u8g2_font_6x10_tr);
    artistView = layoutRow(artist, artistScroll, 43, 206, 0);
    albumView = layoutRow(album, albumScroll, 43, 206, 0);
  } else if (page == PAGE_LOADING || page == PAGE_ERROR || page == PAGE_MESSAGE) {
    u8g2.setFont(u8g2_font_6x10_tr);
    pageView = layoutRow(pageText, pageScroll, 64, 184, 0);
  }

  // U8g2 _1_ uses a small page buffer. State and animation phase remain fixed
  // across all pages in this frame; Serial bytes are only collected, not applied.
  u8g2.firstPage();
  do {
    u8g2.setDrawColor(1);
    if (page == PAGE_PLAYER) {
      u8g2.setFont(u8g2_font_helvB14_tr);
      drawScrollingRow(titleView, 19, 2, 23);
      u8g2.setFont(u8g2_font_5x7_tr);
      u8g2.setCursor(7, 31); u8g2.print(F("ARTIST"));
      u8g2.setCursor(7, 42); u8g2.print(F("ALBUM"));
      u8g2.setFont(u8g2_font_6x10_tr);
      drawScrollingRow(artistView, 31, 23, 34);
      drawScrollingRow(albumView, 42, 34, 45);
      u8g2.drawHLine(7, 49, 242);
      const uint16_t filled = durationSeconds == 0 ? 0 :
        static_cast<uint32_t>(242) * positionSeconds / durationSeconds;
      if (filled > 0) u8g2.drawBox(7, 48, filled, 3);
      u8g2.drawDisc(7 + filled, 49, 2);
      u8g2.setFont(u8g2_font_6x10_tr);
      u8g2.drawStr(7, 61, elapsed);
      u8g2.drawStr(249 - u8g2.getStrWidth(total), 61, total);
      u8g2.setFont(u8g2_font_5x7_tr);
      if (state == PLAYING) {
        u8g2.setCursor(110, 61); u8g2.print(F("PLAYING"));
      } else if (state == PAUSED) {
        u8g2.setCursor(113, 61); u8g2.print(F("PAUSED"));
      } else {
        u8g2.setCursor(110, 61); u8g2.print(F("STOPPED"));
      }
    } else if (page == PAGE_BOOT) {
      const uint16_t progress = frameNow - pageStartMs >= 2400 ? 178 :
        static_cast<uint32_t>(178) * (frameNow - pageStartMs) / 2400;
      u8g2.drawCircle(32, 31, 16);
      u8g2.drawVLine(35, 21, 17);  // musical note
      u8g2.drawHLine(35, 21, 7);
      u8g2.drawDisc(31, 38, 3);
      u8g2.setFont(u8g2_font_helvB14_tr);
      u8g2.setCursor(64, 31); u8g2.print(F("PI PLAYER"));
      u8g2.setFont(u8g2_font_6x10_tr);
      u8g2.setCursor(65, 45); u8g2.print(F("Starting..."));
      u8g2.drawHLine(65, 54, 178);
      if (progress) u8g2.drawBox(65, 53, progress, 3);
    } else if (page == PAGE_LOADING) {
      int8_t dx = 0, dy = -12;
      switch (((frameNow - pageStartMs) / 120) & 7) {
        case 1: dx = 8;  dy = -8; break;
        case 2: dx = 12; dy = 0;  break;
        case 3: dx = 8;  dy = 8;  break;
        case 4: dx = 0;  dy = 12; break;
        case 5: dx = -8; dy = 8;  break;
        case 6: dx = -12;dy = 0;  break;
        case 7: dx = -8; dy = -8; break;
      }
      u8g2.drawCircle(32, 31, 12);
      u8g2.drawDisc(32 + dx, 31 + dy, 3);
      u8g2.setFont(u8g2_font_helvB14_tr);
      u8g2.setCursor(64, 22); u8g2.print(F("LOADING"));
      u8g2.setFont(u8g2_font_6x10_tr);
      drawScrollingRow(pageView, 40, 31, 44);
      u8g2.drawBox(64 + (((frameNow - pageStartMs) / 120) & 7) * 20, 52, 16, 2);
    } else if (page == PAGE_ERROR) {
      u8g2.drawTriangle(33, 11, 51, 47, 15, 47);
      u8g2.setDrawColor(0);
      u8g2.drawBox(32, 23, 3, 13);
      u8g2.drawDisc(33, 41, 2);
      u8g2.setDrawColor(1);
      u8g2.setFont(u8g2_font_helvB14_tr);
      u8g2.setCursor(64, 22); u8g2.print(F("ERROR"));
      u8g2.setFont(u8g2_font_6x10_tr);
      drawScrollingRow(pageView, 40, 31, 44);
    } else if (page == PAGE_CLOCK) {
      char timeText[6] = {'-', '-', ':', '-', '-', '\0'};
      char secondsText[3] = {'-', '-', '\0'};
      if (clockIsSet) {
        timeText[0] = '0' + clockHour / 10;
        timeText[1] = '0' + clockHour % 10;
        timeText[2] = clockSecond & 1 ? ' ' : ':'; // animated colon
        timeText[3] = '0' + clockMinute / 10;
        timeText[4] = '0' + clockMinute % 10;
        secondsText[0] = '0' + clockSecond / 10;
        secondsText[1] = '0' + clockSecond % 10;
      }
      u8g2.setFont(u8g2_font_5x7_tr);
      u8g2.setCursor(8, 11); u8g2.print(F("CLOCK"));
      u8g2.setFont(u8g2_font_helvB14_tr);
      const uint16_t textWidth = u8g2.getStrWidth(timeText) * 2;
      u8g2.drawStrX2((256 - textWidth) / 2, 43, timeText);
      u8g2.setFont(u8g2_font_6x10_tr);
      u8g2.drawStr(225, 45, secondsText);
      u8g2.drawHLine(8, 55, 240);
      if (clockIsSet && clockSecond) u8g2.drawBox(8, 54, clockSecond * 4, 3);
    } else if (page == PAGE_MESSAGE) {
      u8g2.drawFrame(15, 13, 36, 30);
      u8g2.drawLine(22, 43, 17, 49);
      u8g2.drawLine(17, 49, 30, 43);
      const uint8_t activeDot = ((frameNow - pageStartMs) / 450) % 3;
      for (uint8_t i = 0; i < 3; ++i)
        u8g2.drawDisc(25 + i * 8, 28, i == activeDot ? 2 : 1);
      u8g2.setFont(u8g2_font_helvB14_tr);
      u8g2.setCursor(64, 22); u8g2.print(F("MESSAGE"));
      u8g2.setFont(u8g2_font_6x10_tr);
      drawScrollingRow(pageView, 40, 31, 44);
    }
    collectSerial();
  } while (u8g2.nextPage());
}
} // namespace

void setup() {
  Serial.begin(115200);
  u8g2.begin();
  COPY_DEFAULT(title, "Dreams");
  COPY_DEFAULT(artist, "Fleetwood Mac");
  COPY_DEFAULT(album, "Rumours");
  const uint32_t now = millis();
  positionTickMs = lastFrameMs = clockTickMs = pageStartMs = now;
  resetScroll(titleScroll, now);
  resetScroll(artistScroll, now);
  resetScroll(albumScroll, now);
  resetScroll(pageScroll, now);
}

void loop() {
  processSerial();
  const uint32_t now = millis();

  // A disconnected sender cannot leave the screen frozen forever.
  if (inBatch && now - batchStartMs >= 3000) {
    inBatch = false;
    dirty = true;
  }

  while (state == PLAYING && now - positionTickMs >= 1000) {
    positionTickMs += 1000;
    if (positionSeconds < MAX_SECONDS) ++positionSeconds;
    if (page == PAGE_PLAYER) dirty = true;
    if (durationSeconds > 0 && positionSeconds >= durationSeconds) {
      positionSeconds = durationSeconds;
      state = STOPPED; // await the Pi's next track update
    }
  }

  while (clockIsSet && now - clockTickMs >= 1000) {
    clockTickMs += 1000;
    if (++clockSecond == 60) {
      clockSecond = 0;
      if (++clockMinute == 60) {
        clockMinute = 0;
        clockHour = (clockHour + 1) % 24;
      }
    }
    if (page == PAGE_CLOCK) dirty = true;
  }

  if (page == PAGE_BOOT && now - pageStartMs >= 2400)
    showPage(PAGE_PLAYER, nullptr, now);

  if (now - lastFrameMs >= FRAME_MS) {
    lastFrameMs = now;
    bool moved = false;
    if (page == PAGE_PLAYER) {
      moved |= advanceScroll(titleScroll, title, 242, u8g2_font_helvB14_tr, now);
      moved |= advanceScroll(artistScroll, artist, 206, u8g2_font_6x10_tr, now);
      moved |= advanceScroll(albumScroll, album, 206, u8g2_font_6x10_tr, now);
    } else if (page == PAGE_LOADING || page == PAGE_ERROR || page == PAGE_MESSAGE) {
      moved = advanceScroll(pageScroll, pageText, 184, u8g2_font_6x10_tr, now);
    }

    if (page == PAGE_BOOT || page == PAGE_LOADING || page == PAGE_MESSAGE) {
      const uint8_t phase = page == PAGE_MESSAGE ?
        ((now - pageStartMs) / 450) % 3 : ((now - pageStartMs) / 120) & 7;
      if (phase != lastAnimationPhase) {
        lastAnimationPhase = phase;
        dirty = true;
      }
    }

    if (!inBatch && (dirty || moved)) {
      drawScreen();
      dirty = false;
      processSerial(); // apply a message that arrived during the page loop
    }
  }
}