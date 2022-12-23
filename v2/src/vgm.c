#include <stddef.h>
#include <stdint.h>
#include <string.h>
#include <wonderful.h>
#include <ws.h>
#include "vgm.h"
#include "wonderful-asm-common.h"
#include "ws/hardware.h"

void vgmswan_init(vgmswan_state_t *state, uint8_t bank, uint8_t song_id) {
    outportb(IO_BANK_ROM1, bank);
    uint8_t __far* ptr = MK_FP(0x3000, ((uint16_t) song_id) * 3);
    state->pos = ptr[0] | (ptr[1] << 8);
    state->bank = bank + ptr[2];
    state->flags = 0;
}

uint16_t vgmswan_play(vgmswan_state_t *state) {
    uint8_t bank_backup = inportb(IO_BANK_ROM0);
    outportb(IO_BANK_ROM0, state->bank);
    uint8_t __far* ptr = MK_FP(0x2000, state->pos);
    uint16_t result = 0;
    while (result == 0) {
        // play routine! <3
        uint8_t cmd = *(ptr++);
        switch (cmd & 0xE0) {
        case 0x00:
        case 0x20: { // memory write
            uint16_t addr = cmd + (inportb(IO_SND_WAVE_BASE) << 6);
            uint8_t len = *(ptr++);
            memcpy((uint8_t*) addr, ptr, len);
            ptr += len;
        } break;
        case 0x40: { // port write (byte)
            uint8_t v = *(ptr++);
            outportb(cmd ^ 0xC0, v);
        } break;
        case 0x60: { // port write (word)
            uint16_t v = *((uint16_t __far*) ptr); ptr += 2;
            outportw(cmd ^ 0xE0, v);
        } break;
        case 0xE0: { // special
            switch (cmd) {
            case 0xF0:
            case 0xF1:
            case 0xF2:
            case 0xF3:
            case 0xF4:
            case 0xF5:
            case 0xF6: {
                result = cmd - 0xEF;
            } break;
            case 0xF7: {
                outportb(IO_BANK_ROM0, ++state->bank);
                state->pos = 0;
                ptr = MK_FP(0x2000, 0);
            } break;
            case 0xF8: {
                result = *(ptr++);
            } break;
            case 0xF9: {
                result = *((uint16_t __far*) ptr); ptr += 2;
            } break;
            case 0xFA: {
                state->pos = *((uint16_t __far*) ptr); ptr += 2;
                state->bank += *(ptr++);
                outportb(IO_BANK_ROM0, state->bank);
                ptr = MK_FP(0x2000, state->pos);
            } break;
            case 0xFB: {
                uint8_t ctrl = *(ptr++);
                outportb(IO_SDMA_CTRL, 0);
                if (ctrl & 0x80) {
                    // play sample
                    outportw(IO_SDMA_SOURCE_L, *((uint16_t __far*) ptr)); ptr += 2;
                    outportb(IO_SDMA_SOURCE_H, 0x3);
                    outportw(IO_SDMA_COUNTER_L, *((uint16_t __far*) ptr)); ptr += 2;
                    outportb(IO_SDMA_COUNTER_H, 0);
                    outportb(IO_SDMA_CTRL, ctrl);
                }
            } break;
            // TODO: 0xFC-0xFF
            }
        }
        }
    }

    state->pos = (uint16_t) ptr;
    outportb(IO_BANK_ROM0, bank_backup);
    return result;
}
