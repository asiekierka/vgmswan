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
#include "ws/keypad.h"
#include "ws/system.h"

#define SCREEN1 ((uint8_t*) 0x1800)

static const uint8_t __far bank_offsets[] = {
        1, // 0x00 = 2 banks
        3, // 0x01 = 4 banks
        7, // 0x02 = 8 banks
        15, // 0x03 = 16 banks
        31, // 0x04 = 32 banks
        47, // 0x05 = 48 banks
        63, // 0x06 = 64 banks
        95, // 0x07 = 96 banks
        127, // 0x08 = 128 banks
        255  // 0x09 = 256 banks
};


/* void ui_puts(uint8_t x, uint8_t y, uint8_t color, const char __far* loc_buf) {
    y++;
    while (*loc_buf != 0) {
        ws_screen_put(SCREEN1, *(loc_buf++), x++, y);
    }
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

uint16_t keys_held_last = 0;
uint8_t vgm_bank, vgm_song_id, vgm_song_count;

void reset_song(void) {
    cpu_irq_disable();

    // reset some audio registers
    outportb(IO_SND_CH_CTRL, 0);

    samples_played = 0;
    vgmswan_init(&vgm_state, vgm_bank, vgm_song_id);

    for (uint8_t i = 0; i < vgm_song_count; i++) {
        ws_screen_put(SCREEN1,    
            ((i == vgm_song_id ? 3 : 1) << 9) | (i < 9 ? ('1'+i) : ('A'+i-9)),
            i + 5, 18
        );
    }

    outportw(IO_HBLANK_TIMER, 3);
    outportw(IO_TIMER_CTRL, 0x01);

    ws_hwint_set(HWINT_HBLANK_TIMER | HWINT_VBLANK);
    cpu_irq_enable();
}

void  __attribute__((interrupt)) vbl_interrupt_handler(void) {
    uint32_t samples_played_local = samples_played;

    uint16_t ch1_freq = inportw(IO_SND_FREQ_CH1);
    uint16_t ch2_freq = inportw(IO_SND_FREQ_CH2);
    uint16_t ch3_freq = inportw(IO_SND_FREQ_CH3);
    uint16_t ch4_freq = inportw(IO_SND_FREQ_CH4);
    uint8_t ch1_vol = inportb(IO_SND_VOL_CH1);
    uint8_t ch2_vol = inportb(IO_SND_VOL_CH2);
    uint8_t ch3_vol = inportb(IO_SND_VOL_CH3);
    uint8_t ch4_vol = inportb(IO_SND_VOL_CH4);
    uint8_t ch_ctrl = inportb(IO_SND_CH_CTRL);

    uint16_t keys_held = ws_keypad_scan();
    uint16_t keys_pressed = keys_held & (~keys_held_last);
    keys_held_last = keys_held;
    
    ws_hwint_ack(HWINT_VBLANK);

    if (keys_pressed & KEY_X4) {
        if (vgm_song_id > 0) {
            vgm_song_id--;
            reset_song();
        }
    } else if (keys_pressed & KEY_X2) {
        if (vgm_song_id < (vgm_song_count - 1)) {
            vgm_song_id++;
            reset_song();
        }
    }

    cpu_irq_enable();

    if ((vbl_ticks & 3) == 0) drop_sound_levels();
    if (ch_ctrl & 0x01) mark_sound_level(ch1_freq, MAX_VOL(ch1_vol));
    if (ch_ctrl & 0x02) mark_sound_level(ch2_freq, MAX_VOL(ch2_vol));
    if (ch_ctrl & 0x04) mark_sound_level(ch3_freq, MAX_VOL(ch3_vol));
    if (ch_ctrl & 0x08) mark_sound_level(ch4_freq, MAX_VOL(ch4_vol));

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

    if (ws_mode_set(WS_MODE_COLOR)) {
        MEM_COLOR_PALETTE(0)[0] = 0x000;
        MEM_COLOR_PALETTE(1)[0] = 0x333;
        MEM_COLOR_PALETTE(1)[1] = 0x8EF;
        MEM_COLOR_PALETTE(2)[0] = 0x333;
        MEM_COLOR_PALETTE(2)[1] = 0xFFF;
        MEM_COLOR_PALETTE(3)[0] = 0x8EF;
        MEM_COLOR_PALETTE(3)[1] = 0x333;

        MEM_COLOR_PALETTE(12)[1] = 0x0F0;
        MEM_COLOR_PALETTE(13)[1] = 0x0F0;
        MEM_COLOR_PALETTE(14)[1] = 0xFF0;
        MEM_COLOR_PALETTE(15)[1] = 0xF00;
    } else {
        outportw(0x20, 0x0000);
        outportw(0x22, 0x0052);
        outportw(0x24, 0x0072);
        outportw(0x26, 0x0027);
        outportw(0x38, 0x0077);
        outportw(0x3A, 0x0077);
        outportw(0x3C, 0x0044);
        outportw(0x3E, 0x0022);
    }

    ws_screen_put(SCREEN1, (2 << 9) | 'S', 0, 18);
    ws_screen_put(SCREEN1, (2 << 9) | 'o', 1, 18);
    ws_screen_put(SCREEN1, (2 << 9) | 'n', 2, 18);
    ws_screen_put(SCREEN1, (2 << 9) | 'g', 3, 18);
    for (uint8_t i = 4; i < 28; i++) {
        ws_screen_put(SCREEN1, (1 << 9) | ' ', i, 18);
    }
    ws_screen_put(SCREEN1, (1 << 9) | ':', 25, 18);

    ws_hwint_set_handler(HWINT_IDX_HBLANK_TIMER, vgm_interrupt_handler);
    ws_hwint_set_handler(HWINT_IDX_VBLANK, vbl_interrupt_handler);

    // figure out initial bank
    vgm_bank = ((inportb(IO_BANK_LINEAR) << 4) | 0x0F) - bank_offsets[*((uint8_t __far*) MK_FP(0xFFFF, 0x000A))];

    // count songs
    vgm_song_count = 0;
    outportb(IO_BANK_ROM0, vgm_bank);
    uint8_t __far* ptr = MK_FP(0x2000, 2);
    while (*ptr != 0xFF) {
        vgm_song_count++;
        ptr += 3;
    }
    
    vgm_song_id = 0;

    outportb(IO_SND_WAVE_BASE, SND_WAVE_BASE(0x1800));
    outportb(IO_SND_OUT_CTRL, 0x0F);

    outportw(0x20, 0x5270);
    outportb(IO_SCR1_SCRL_Y, 8);
    outportb(IO_SCR_BASE, SCR1_BASE(0x1800));
    outportw(IO_DISPLAY_CTRL, DISPLAY_SCR1_ENABLE);

    reset_song();

    while(true) cpu_halt();
}
