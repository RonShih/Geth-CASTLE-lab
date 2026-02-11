# Loading required packages
if (require("scales")) {
    print("scales is loaded correctly")
} else {
    print("trying to install scales")
    install.packages("scales")
    if (require(scales)) {
        print("scales installed and loaded")
    } else {
        stop("could not install scales")
    }
}

if (require("ggplot2")) {
    print("ggplot2 is loaded correctly")
} else {
    print("trying to install ggplot2")
    install.packages("ggplot2")
    if (require(ggplot2)) {
        print("ggplot2 installed and loaded")
    } else {
        stop("could not install ggplot2")
    }
}

if (require("RColorBrewer")) {
    print("RColorBrewer is loaded correctly")
} else {
    print("trying to install RColorBrewer")
    install.packages("RColorBrewer")
    if (require(RColorBrewer)) {
        print("RColorBrewer installed and loaded")
    } else {
        stop("could not install RColorBrewer")
    }
}

if (require("extrafont")) {
    print("extrafont is loaded correctly")
} else {
    print("trying to install extrafont")
    install.packages("extrafont")
    if (require(extrafont)) {
        print("extrafont installed and loaded")
    } else {
        stop("could not install extrafont")
    }
}

if (require("grid")) {
    print("grid is loaded correctly")
} else {
    print("trying to install grid")
    install.packages("grid")
    if (require(grid)) {
        print("grid installed and loaded")
    } else {
        stop("could not install grid")
    }
}

# Ensure dplyr is loaded
if (require("dplyr")) {
    print("dplyr is loaded correctly")
} else {
    print("trying to install dplyr")
    install.packages("dplyr")
    if (require(dplyr)) {
        print("dplyr installed and loaded")
    } else {
        stop("could not install dplyr")
    }
}

# Loading the necessary libraries
library(grid)
library(ggplot2)
library(extrafont)
library(dplyr) # Ensure dplyr is loaded
require(scales)

# Load fonts
font_import()
loadfonts()

# Set plot dimensions and styles
mywidth <- 5
myheight <- 3
colorManual <- c("#C1121F")
my_line <- c("solid")
my_shape <- c(6)

# Reading data from input file (using commandArgs for input)
if (T) {
    args <- commandArgs(trailingOnly = TRUE)

    # Assuming the data is in the first argument file path
    x1 <- read.table(args[1], header = TRUE)
    x1_filtered <- x1 %>% filter(count > 0)
    min_x <- min(x1_filtered$bucket)
    max_x <- max(x1_filtered$bucket)
    vline_labels <- data.frame(
        xintercept = c(min_x, max_x),
        label = c(paste0("  ", min_x), paste0("  ", max_x)),
        hjust = c(1, 0),
        nudge_x = c(-3, -1)
    )
    print(vline_labels)
    # Output file name is given as the second argument
    cairo_pdf(file = args[2], width = mywidth, height = myheight)

    # Plotting the data
    ggplot(data = x1_filtered, aes(x = bucket, y = count)) +
        geom_point(size = 1.5, stroke = 1.5, fill = "white", colour = colorManual) +
        geom_vline(xintercept = c(min_x, max_x), linetype = "dashed", color = "black", linewidth = 0.5) +
        geom_text(
            data = vline_labels,
            aes(x = xintercept, y = max(x1$count) + 19600000, label = label),
            hjust = vline_labels$hjust,
            vjust = 1.5,
            colour = "black",
            size = 5,
            angle = 0,
            nudge_x = vline_labels$nudge_x
        ) +
        scale_shape_manual(values = c(my_shape)) +
        coord_cartesian(ylim = c(0, max(x1$count) + 14000000), xlim = c(0, 106)) +
        scale_x_continuous(expand = c(0, 0), breaks = c(0, 50, 100), labels = format(c(0, 50, 100), scientific = FALSE)) +
        scale_y_continuous(expand = c(0, 0), breaks = seq(0, 300000000, 100000000), labels = format(seq(0, 300, 100), scientific = FALSE)) +
        ylab("Count (M)") +
        xlab("KV size (byte)") +
        theme_bw() +
        theme(
            panel.grid.major = element_blank(), panel.grid.minor = element_blank(),
            panel.background = element_blank(),
            panel.border = element_blank(),
            axis.line = element_line(colour = "black", linewidth = 0.15),
            axis.ticks = element_line(linewidth = 0.15),
            axis.text.x = element_text(margin = margin(5, 0, 0, 0), angle = 0, hjust = 0.5, colour = "black", size = 20),
            axis.title.y = element_text(size = 19, hjust = 0.5),
            axis.text.y = element_text(margin = margin(0, 2, 0, 0), colour = "black", size = 20),
            axis.title.x = element_text(size = 20),
            legend.key.size = unit(0.5, "cm"),
            legend.title = element_blank(),
            legend.position = "none",
            legend.margin = margin(t = 0, unit = "cm"),
            legend.direction = "horizontal",
            legend.box = "horizontal",
            legend.text = element_text(size = 16.5),
            plot.margin = unit(c(0.1, 0.1, 0.1, 0.1), "cm")
        )
}
