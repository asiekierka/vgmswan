#include <stdarg.h>
#include <stdbool.h>
#include <stdint.h>
#include <string.h>
#include <ws.h>
#include "../obj/font.bmh"
#include "vgm.h"
#include "wonderful-asm-common.h"
#include "ws/display.h"
#include "ws/hardware.h"
#include "ws/system.h"

#define SCREEN1 ((uint8_t*) 0x1800)

void ui_puts(uint8_t x, uint8_t y, uint8_t color, const char __far* loc_buf) {
    y++;
    while (*loc_buf != 0) {
        ws_screen_put(SCREEN1, *(loc_buf++), x++, y);
    }
}

/* #include "nanoprintf.h"

void ui_printf(uint8_t x, uint8_t y, uint8_t color, const char __far* format, ...) {
    char buf[33];
    va_list val;
    va_start(val, format);
    npf_vsnprintf(buf, sizeof(buf), format, val);
    va_end(val);
    const char *loc_buf = buf;
    y++;
    while (*loc_buf != 0) {
        ws_screen_put(SCREEN1, *(loc_buf++), x++, y);
    }
}

static uint8_t cy = 2;

void dprint(const char __far* format, ...) {
    char buf[33];
    va_list val;
    va_start(val, format);
    npf_vsnprintf(buf, sizeof(buf), format, val);
    va_end(val);
    const char *loc_buf = buf;
    while ((cy & 0x1F) < 1) cy++;
    uint8_t cx = 0;
    while (*loc_buf != 0) {
        ws_screen_put(SCREEN1, *(loc_buf++), cx++, cy);
    }
    cy++;
} */

static void vgm_sample_wait(uint16_t samples) {
    // samples to lines
    uint16_t lines = ((((uint32_t) (samples)) * 120) + 440) / 441;
    // uint16_t lines = samples >> 2;
    if (lines > 0) {
        outportw(IO_HBLANK_TIMER, lines);
        outportw(IO_TIMER_CTRL, 0x01);
        cpu_halt();
        outportw(IO_TIMER_CTRL, 0x00);
    }
}

static vgmswan_state_t vgm_state;
static volatile uint32_t samples_played;

void  __attribute__((interrupt)) vgm_interrupt_handler(void) {
    while (true) {
        uint16_t result = vgmswan_play(&vgm_state);
        if (result > 0) {
            if (result != VGMSWAN_PLAYBACK_FINISHED) {
                samples_played += result;
                outportw(IO_TIMER_CTRL, 0);
                outportw(IO_HBLANK_TIMER, result);
                outportw(IO_TIMER_CTRL, HBLANK_TIMER_ENABLE);
            }
            ws_hwint_ack(HWINT_HBLANK_TIMER);
            return;
        }
    }
}

uint8_t vbl_ticks = 0;
uint8_t sound_levels[32] = {0};

static void drop_sound_levels(void) {
    for (uint8_t i = 0; i < 28; i++) {
        if (sound_levels[i] > 0) {
            ws_screen_put(SCREEN1, ' ', i, 17 - sound_levels[i]);
            sound_levels[i]--;
        }
    }
}

static void mark_sound_level(uint16_t freq, uint8_t vol) {
    uint8_t ix = freq / 73;
    if (sound_levels[ix] < vol) {
        sound_levels[ix] = vol;
        for (uint8_t iy = 1; iy <= vol; iy++) {
            ws_screen_put(SCREEN1, 31 | ((12 + (iy >> 2)) << 9), ix, 17 - iy);
        }
    }
}

static inline uint8_t MAX_VOL(uint8_t v) {
    uint8_t v1 = v & 0xF;
    uint8_t v2 = v >> 4;
    return v1 > v2 ? v1 : v2;
}

void  __attribute__((interrupt)) vbl_interrupt_handler(void) {
    uint32_t samples_played_local = samples_played;

    outportb(IO_BANK_ROM0, vgm_state.start_bank);
    uint32_t samples_total = *((uint32_t __far*) MK_FP(0x2000, 0x18));

    uint16_t ch1_freq = inportw(IO_SND_FREQ_CH1);
    uint16_t ch2_freq = inportw(IO_SND_FREQ_CH2);
    uint16_t ch3_freq = inportw(IO_SND_FREQ_CH3);
    uint16_t ch4_freq = inportw(IO_SND_FREQ_CH4);
    uint8_t ch1_vol = inportb(IO_SND_VOL_CH1);
    uint8_t ch2_vol = inportb(IO_SND_VOL_CH2);
    uint8_t ch3_vol = inportb(IO_SND_VOL_CH3);
    uint8_t ch4_vol = inportb(IO_SND_VOL_CH4);
    uint8_t ch_ctrl = inportb(IO_SND_CH_CTRL);

    ws_hwint_ack(HWINT_VBLANK);
    cpu_irq_enable();

    if ((vbl_ticks & 3) == 0) drop_sound_levels();
    if (ch_ctrl & 0x01) mark_sound_level(ch1_freq, MAX_VOL(ch1_vol));
    if (ch_ctrl & 0x02) mark_sound_level(ch2_freq, MAX_VOL(ch2_vol));
    if (ch_ctrl & 0x04) mark_sound_level(ch3_freq, MAX_VOL(ch3_vol));
    if (ch_ctrl & 0x08) mark_sound_level(ch4_freq, MAX_VOL(ch4_vol));

    samples_total = (samples_total * 120 / 441);

    /* for (uint8_t i = 1; i < 21; i++) {
        ws_screen_put(SCREEN1, (1 << 9) | '=', i, 18);
    } */
    uint32_t s_pos = (samples_played_local * 20 / samples_total);
    if (s_pos >= 20) s_pos = 19;
    ws_screen_put(SCREEN1, (2 << 9) | '#', s_pos+1, 18);

    uint16_t s_seconds = (samples_played_local / 12000);
    ws_screen_put(SCREEN1, (2 << 9) | ('0' + (s_seconds % 10)), 27, 18); s_seconds /= 10;
    ws_screen_put(SCREEN1, (2 << 9) | ('0' + (s_seconds % 6)), 26, 18); s_seconds /= 6;
    ws_screen_put(SCREEN1, (2 << 9) | ('0' + (s_seconds % 10)), 24, 18); s_seconds /= 10;
    ws_screen_put(SCREEN1, (2 << 9) | ('0' + (s_seconds % 10)), 23, 18);

    vbl_ticks++;
}

int main(void) {
    memcpy((uint8_t*) 0x2000, bmp_font, bmp_font_size);
    memset((uint8_t*) 0x1800, 0, 0x800);
    ws_display_set_shade_lut(SHADE_LUT(0, 2, 4, 6, 8, 11, 13, 15));

    ws_mode_set(WS_MODE_COLOR);
    MEM_COLOR_PALETTE(0)[0] = 0x000;
    MEM_COLOR_PALETTE(1)[0] = 0x333;
    MEM_COLOR_PALETTE(1)[1] = 0x8EF;
    MEM_COLOR_PALETTE(2)[0] = 0x333;
    MEM_COLOR_PALETTE(2)[1] = 0xFFF;

    MEM_COLOR_PALETTE(12)[1] = 0x0F0;
    MEM_COLOR_PALETTE(13)[1] = 0x0F0;
    MEM_COLOR_PALETTE(14)[1] = 0xFF0;
    MEM_COLOR_PALETTE(15)[1] = 0xF00;

    ws_screen_put(SCREEN1, (1 << 9) | '[', 0, 18);
    ws_screen_put(SCREEN1, (1 << 9) | ']', 21, 18);
    ws_screen_put(SCREEN1, (1 << 9) | ':', 25, 18);
    ws_screen_put(SCREEN1, (1 << 9) | ' ', 22, 18);
    for (uint8_t i = 1; i < 21; i++) {
        ws_screen_put(SCREEN1, (1 << 9) | '=', i, 18);
    }

    outportw(0x20, 0x5270);
    outportb(IO_SCR1_SCRL_Y, 8);
    outportb(IO_SCR_BASE, SCR1_BASE(0x1800));
    outportw(IO_DISPLAY_CTRL, DISPLAY_SCR1_ENABLE);

    outportb(IO_SND_WAVE_BASE, SND_WAVE_BASE(0x1800));
    outportb(IO_SND_OUT_CTRL, 0x0F);

    bool inited = false;

    {
        volatile uint8_t __far* rom_ptr = MK_FP(0x2000, 0);
        uint8_t i = (inportb(IO_BANK_LINEAR) << 4) | 0xF;
        for (; i >= 0x80; i--) {
            outportb(IO_BANK_ROM0, i);
            if (rom_ptr[0] == 0x56 && rom_ptr[1] == 0x67 && rom_ptr[2] == 0x6d && rom_ptr[3] == 0x20) {
                vgmswan_init(&vgm_state, i, 0);
                inited = true;
                break;
            }
        }
    }

    if (!inited) {
        ui_puts(0, 0, 0, "vgm data not found");
        while(true) cpu_halt();
    }

    samples_played = 0;
    outportw(IO_HBLANK_TIMER, 2);
    outportw(IO_TIMER_CTRL, 0x01);

    ws_hwint_set_handler(HWINT_IDX_HBLANK_TIMER, vgm_interrupt_handler);
    ws_hwint_set_handler(HWINT_IDX_VBLANK, vbl_interrupt_handler);
    ws_hwint_enable(HWINT_HBLANK_TIMER | HWINT_VBLANK);
    cpu_irq_enable();

    while(true) cpu_halt();
}
