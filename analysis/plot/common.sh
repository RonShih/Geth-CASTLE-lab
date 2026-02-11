#!/bin/bash

crop_func() {
  prefix=`echo $1 | rev | cut -d. -f2- | rev`
  cmd="pdfcrop"
  which $cmd
  if [[ $? -ne 0 ]]; then
    cmd="pdfcrop.exe"
  fi
  echo $cmd
  $cmd $1
  mv ${prefix}-crop.pdf $1
  echo "finish crop $1"
}

crop_and_remove_fonts_func() {
  prefix=`echo $1 | rev | cut -d. -f2- | rev`
  cmd="pdfcrop"
  which $cmd
  if [[ $? -ne 0 ]]; then
    cmd="pdfcrop.exe"
  fi
  echo $cmd
  $cmd $1
  mv ${prefix}-crop.pdf $1
  gs -o output.pdf -dNoOutputFonts -sDEVICE=pdfwrite $1
  mv output.pdf $1
  echo "finish crop $1"
} 

