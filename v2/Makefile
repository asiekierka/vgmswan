ifndef WONDERFUL_TOOLCHAIN
$(error Please define WONDERFUL_TOOLCHAIN to point to the location of the Wonderful toolchain.)
endif
include $(WONDERFUL_TOOLCHAIN)/i8086/wswan.mk

BINFILE := converter/output.bin

TARGET := $(notdir $(shell pwd)).wsc
OBJDIR := obj
SRCDIRS := src
RESDIRS := res
MKDIRS := $(OBJDIR)
LIBS := -lws -lc -lgcc
CFLAGS += -O2 -fno-function-sections

CSOURCES := $(foreach dir,$(SRCDIRS),$(notdir $(wildcard $(dir)/*.c)))
ASMSOURCES := $(foreach dir,$(SRCDIRS),$(notdir $(wildcard $(dir)/*.S)))
PNGASSETS := $(foreach dir,$(RESDIRS),$(notdir $(wildcard $(dir)/*.png)))
EARLY_OBJECTS := $(PNGASSETS:%.png=$(OBJDIR)/%.png.o)
OBJECTS := $(EARLY_OBJECTS) $(CSOURCES:%.c=$(OBJDIR)/%.o) $(ASMSOURCES:%.S=$(OBJDIR)/%.o)

DEPS := $(OBJECTS:.o=.d)
CFLAGS += -MMD -MP

vpath %.c $(SRCDIRS)
vpath %.S $(SRCDIRS)
vpath %.png $(RESDIRS)

.PHONY: all clean install

all: $(TARGET)

$(TARGET): $(OBJECTS)
	$(SWANLINK) -v -o $@ -a $(BINFILE) --heap-length 0x1800 --color --output-elf $@.elf --linker-args $(LDFLAGS) $(WF_CRT0) $(OBJECTS) $(LIBS)

$(OBJDIR)/%.png.c: %.png | $(OBJDIR)
	$(SUPERFAMICONV) tiles -i $(basename $<).png -p $(basename $<).json -M ws -D -F -d $(OBJDIR)/$(notdir $<).bin
	$(BIN2C) --header $(basename $(basename $@)).bmh $@ bmp_$(basename $(notdir $<)):$(OBJDIR)/$(notdir $<).bin

$(OBJDIR)/%.o: %.c | $(OBJDIR)
	$(CC) $(CFLAGS) -c -o $@ $<

$(OBJDIR)/%.o: %.S | $(OBJDIR)
	$(CC) $(CFLAGS) -c -o $@ $<

$(OBJDIR):
	$(info $(shell mkdir -p $(MKDIRS)))

clean:
	rm -r $(OBJDIR)/*
	rm $(TARGET) $(TARGET).elf

-include $(DEPS)
